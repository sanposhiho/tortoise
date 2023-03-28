## Concept

The resource management in Kubernetes world is difficult today,
there are many options on your table (HPA, VPA, KEDA, etc), 
and you want to reduce the wasted resources as long as possible, 
but at the same time, you need to keep the reliability of workloads.

Tortoise, it aims to do such complicated configuration by system
- give general recommended values to Autoscalers from the controller and keep update them.
- use historical resource usage of target workloads to make sure the safety.
- expose little configuration to users.

## Side Notes

It's implemented based on our experience in Mercari.

- Our workloads are mostly Golang HTTP/GRPC server.
- Our workloads mostly get traffic from people in the same timezone, and the demand of resources is usually very similar to the same time one week ago.

Depending on how your workloads look like, tortoise may or may not fit your workloads.
