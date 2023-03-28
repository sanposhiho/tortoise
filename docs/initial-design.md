## Tortoise; Kubernetes resource optimizer

Author: Kensei Nakada (@sanposhiho)

### Summary

Create the custom resource called PPA to manage both Pod’s resource request/limit and autoscaling configuration.

Users can configure only:
- The minimum resource request on each container.
- The way to autoscale; vertical or horizontal.
- Emergency mode; change the Pod number high enough.

PPA's basic approach is to let users configure a few settings, freeing them from complex resource management tasks. 
The PPA controller will manage HPA and VPA automatically, adjusting resource requests/limits for safe and efficient resource usage.

### Motivation

The resource optimization with HPA is difficult.
Especially, when it comes to the multiple container Pods, it’s more than difficult.

- Need to adjust the HPA’s target utilization.
- Need to adjust the minReplicas not to be high.
- Need to adjust the maxReplicas.
- Need to adjust all container’s utilization by changing the resource request as well.
  - Let’s say, the first container’s utilization is always high and the second container’s utilization is always low, 
the first container’s utilization always kicks the HPA to increase the replica. 
The resource given to the second container could be wasted.

So, we often do these adjustments while looking the past resource utilization etc.

The question here is, 
why do humans need to configure the target by checking the metrics etc? 
The system should be able to configure this better than humans.

From the users PoV, what they really need to define around the resources is:

- The minimum resource request on each container.
- The way to autoscale; vertical or horizontal.
- Emergency mode; change the Pod number high enough.

This PPA makes autoscaling simpler by reducing what humans need to configure,
and archive fully-optimized autoscaler/resource definitions by the system.

Users no longer need to follow recommender, no longer need to try optimization on HPA etc

—

You may say “Okay, this PPA is kinda complicated 🤔” after reading this proposal.
I agree with that. But, that means humans actually need to handle all of these complicated stuff if they want to fully optimize resources.

## Design

Here describes how it’s gonna look like. Not going into much technical detail.

### API design

```yaml
apiVersion: mercari.in/v1alpha1
kind: PPA  # TODO: may change it to a cool name.
    metadata:
        name: echo-jp
        namespace: kouzou-echo-jp-dev // namespaced resource
spec:
    mode: "normal"  // "normal" by default. Here could be "emergency" and then PPA will increase the replicas to high enough value.
    targetRefs:
        deployment: “echo-deployment”    
        horizontalPodAutoscaler: “echo-hpa”
        verticalPodAutoscalers:
        - containerName: “istio-proxy”
          verticalPodAutoscaler:
            cpu: “echo-istio-cpu-vpa”
            memory: “echo-istio-memory-vpa”
        - containerName: “istio-proxy”
          verticalPodAutoscaler:
            cpu: “echo-app-cpu-vpa”
            memory: “echo-app-memory-vpa”
    container:  # required for all containers in deployment.
      - containerName: “istio-proxy”
      # …
      - containerName: 'echo-app'
        MinAllocatedResources:  // PPA won’t give the resource request less than these values to container.
          memory: 1Gi
          cpu: 2
        strategy:
          autoscaling:
            memory: “vertical”  // “vertical” by default for memory.
            cpu: “horizontal”     // “horizontal” by default for cpu.
          updateMode: “Auto” // or “Off” (= dry-run). “Off” by default.
status:
// …
```

It does not mean to hide HPA and VPA behind PPA. Users can view HPA and VPA as they’re doing now.

But, there are some fields managed by PPA in HPA/VPA; Changing them directly in HPA isn’t a good idea. (The controller will soon just overwrite the change.)

But, they can modify other fields like adding their custom metrics, scale down policy etc on HPA by themselves.

### Managed fields on HPA

- minReplicas
- maxReplicas

[if the container resource metric is available]
- `type: ContainerResource` for all containers

[if the container resource metric isn’t available]
The datadog metric for all containers
- `type: Resource`

### Managed fields on VPA

- `.spec.ResourcePolicy.ContainerPolicies[*].MinAllowed`
- `.spec.Recommenders`

## Technical details

Here describes how the PPA actually modifies HPA/VPA and resource requests/limits.

### Horizontal

When horizontal is specified
- PPA manages some fields in HPA.
- PPA manages the Pod’s resource request and limit via VPA
  - Yes, so even if users set “horizontal”, each Pod’s size may sometimes get changed.
  - It’s especially for the multiple containers Pod.
- PPA manages some fields in VPA.

From here, I'd describe how to adjust each field

#### minReplicas

Motivation: avoid setting high minReplicas.

It’s calculated from the last week’s replica number.
(The same as the datadog metric added in these PRs)

Note that we won’t reduce minReplicas to less than 3 to make Pods stronger against the Node failure.

#### maxReplicas

Motivation: set fair maxReplicas and use this value in an emergency mode.

It’s calculated from `2 * the max replica number` over a long time (like 1 month etc)
per-container target utilization

Motivation: set the container target utilization as high as possible based on the historical resource usage.

[if the container resource metric is available]
- `type: ContainerResource` for all containers
[if the container resource metric isn’t available]
- The datadog metric for all containers

They're calculated from the way described in The VPA-based HPA configuration recommender

`type: Resource` is only added if the container resource metric isn’t available. It’s calculated by the average of target utilizations of each container.

#### How to modify the HPA/VPA

It’s enough to just compare the current HPA/VPA and the recommended value and updating HPA/VPA if they are different.
But, the performance of PPA controller shouldn’t be critical for autoscaling because HPA/VPA itself can work only by the HPA/VPA controller.

#### Pod’s resource request and limit

##### Strategy 1: The multiple containers resource optimization

If Pod has multiple containers, then we need to keep adjusting the resource request based on the historical usage.

Example:

Let’s say,
- The istio-proxy has 4 cores resource request, and always consumes 2 cores. (50%)
- The istio-proxy CPU target utilization is 80%.
- The app container has 5 cores resource request, and always consumes 4 cores. (80%)
- The app container CPU target utilization is 80%.

This case, HPA performs scale up mostly by the app container’s target utilization.
Meaning, the istio-proxy always has CPUs more than it actually needs.

We can change the resource request on each container to:
- The istio-proxy: 2.5 cores.
- The app container: 5 cores.

##### Strategy 2: The smaller Pods resource optimization

If the service is small, it may reach `minReplicas` at off-peak time.
In such cases, we need to reduce the resource request. Use VPA recommended value to reduce the Pod size during such off-peak time.
Once the number of Pods almost always goes above 3 or the resource request cannot reduce more due to the MinAllocatedResources limitation, then finish the size optimization.

#### How to modify the resource request

As described in above “prerequisite” section, users should have VPAs for each container’s each resource.
And, we can directly modify the VPA’s recommendation value in VPA.status.
(By changing .spec.Recommenders, we can stop VPA to generate the recommendation in VPA’s status.)

We modify the VPA’s status directly and let VPA replace the Pods if Pod has more/less resources than the recommendation.
Then, we don’t need to implement something similar to VPA to update the Pod resource request.

### Vertical

When vertical is specified, PPA updates the corresponding VPA’s .spec.ResourcePolicy.ContainerPolicies[*].MinAllowed to be the same as PPA’s MinAllocatedResources.
Basically, that’s it. Almost all stuff should be done by VPA itself.

#### The replica number adjustment

The number of replicas Pod’s size could be super bigger depending on the definition of PPA.
In the worst-case scenario, Pod size gets bigger than the Node size, and no Pods could be scheduled.


We should define the upper limit on each VPA so that we can prevent such cases.

### Emergency Mode

Due to some incident, we sometimes want to increase the replica number.
This mergency mode is to increase replicas to maxReplicas in such cases,
and to decrease replicas to original value gradually after the incident.

The PPA controller should have the global value for emergency mode which overwrites all PPA’s emergency mode. The platform team will use it when we need to increase all replica’s numbers. (like DDoS, whole traffic increase etc)

## Risks and Mitigations

- The bug on this controller may directly result in service failure.
  - We need to run with a dry-run mode for long enough time so that we can confirm the behavior before actually shipping this out.