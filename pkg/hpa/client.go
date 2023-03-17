package hpa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sanposhiho/tortoise/pkg/annotation"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	v2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/types"

	autoscalingv1alpha1 "github.com/sanposhiho/tortoise/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Client struct {
	c client.Client
}

func New(c client.Client) Client {
	return Client{c: c}
}

const TortoiseHPANamePrefix = "tortoise-hpa-"

func TortoiseVPAName(tortoiseName string) string {
	return TortoiseHPANamePrefix + tortoiseName
}

func (c *Client) GetHPAOnTortoise(ctx context.Context, tortoise *autoscalingv1alpha1.Tortoise) (*v2.HorizontalPodAutoscaler, error) {
	hpa := &v2.HorizontalPodAutoscaler{}
	if err := c.c.Get(ctx, types.NamespacedName{Namespace: tortoise.Namespace, Name: tortoise.Spec.TargetRefs.HorizontalPodAutoscalerRef}, hpa); err != nil {
		return nil, fmt.Errorf("failed to get hpa on tortoise: %w", err)
	}
	return hpa, nil
}

func (c *Client) UpdateHPAFromTortoiseRecommendation(ctx context.Context, tortoise *autoscalingv1alpha1.Tortoise, now time.Time) (*v2.HorizontalPodAutoscaler, error) {
	hpa := &v2.HorizontalPodAutoscaler{}
	if err := c.c.Get(ctx, types.NamespacedName{Namespace: tortoise.Namespace, Name: tortoise.Spec.TargetRefs.HorizontalPodAutoscalerRef}, hpa); err != nil {
		return nil, fmt.Errorf("failed to get hpa on tortoise: %w", err)
	}

	for _, t := range tortoise.Status.Recommendations.HPA.TargetUtilizations {
		for k, r := range t.TargetUtilization {
			if err := updateHPATargetValue(hpa, t.ContainerName, k, r); err != nil {
				return nil, fmt.Errorf("update HPA from the recommendation from tortoise")
			}
		}
	}

	min, err := getReplicasRecommendation(tortoise.Status.Recommendations.HPA.MinReplicas, now)
	if err != nil {
		return nil, fmt.Errorf("get minReplicas recommendation: %w", err)
	}
	hpa.Spec.MinReplicas = &min

	max, err := getReplicasRecommendation(tortoise.Status.Recommendations.HPA.MaxReplicas, now)
	if err != nil {
		return nil, fmt.Errorf("get maxReplicas recommendation: %w", err)
	}
	hpa.Spec.MaxReplicas = max

	return hpa, c.c.Update(ctx, hpa)
}

// getReplicasRecommendation finds the corresponding recommendations.
func getReplicasRecommendation(recommendations []autoscalingv1alpha1.ReplicasRecommendation, now time.Time) (int32, error) {
	for _, r := range recommendations {
		if now.Compare(r.From.Time) >= 0 && now.Compare(r.To.Time) < 0 {
			return r.Value, nil
		}
	}
	return 0, errors.New("no recommendation slot")
}

func updateHPATargetValue(hpa *v2.HorizontalPodAutoscaler, containerName string, k corev1.ResourceName, targetValue int32) error {
	for _, m := range hpa.Spec.Metrics {
		if m.Type != v2.ContainerResourceMetricSourceType {
			continue
		}

		if m.ContainerResource == nil {
			// shouldn't reach here
			klog.ErrorS(nil, "invalid container resource metric", klog.KObj(hpa))
			continue
		}

		if m.ContainerResource.Container != containerName || m.ContainerResource.Name != k || m.ContainerResource.Target.AverageUtilization == nil {
			continue
		}

		m.ContainerResource.Target.AverageUtilization = &targetValue
	}

	var prefix string
	switch k {
	case corev1.ResourceCPU:
		prefix = hpa.GetAnnotations()[annotation.HPAContainerBasedCPUExternalMetricNamePrefixAnnotation]
	case corev1.ResourceMemory:
		prefix = hpa.GetAnnotations()[annotation.HPAContainerBasedMemoryExternalMetricNamePrefixAnnotation]
	default:
		return fmt.Errorf("non supported resource type: %s", k)
	}
	externalMetricName := prefix + containerName

	for _, m := range hpa.Spec.Metrics {
		if m.Type != v2.ExternalMetricSourceType {
			continue
		}

		if m.External == nil {
			// shouldn't reach here
			klog.ErrorS(nil, "invalid external metric", klog.KObj(hpa))
			continue
		}

		if m.External.Metric.Name != externalMetricName {
			continue
		}

		m.External.Target.Value.Set(int64(targetValue))
	}

	return nil
}
