package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/hirrrooo/pangolin-blueprint-controller/internal/atomicfile"
	"github.com/hirrrooo/pangolin-blueprint-controller/internal/blueprint"
)

type Controller struct {
	serviceInformer cache.SharedIndexInformer
	policyInformer  cache.SharedIndexInformer
	policyNamespace string
	output          string
	debounce        time.Duration
	logger          *slog.Logger
	trigger         chan struct{}
	ready           atomic.Bool
}

func New(serviceInformer, policyInformer cache.SharedIndexInformer, policyNamespace, output string, debounce time.Duration, logger *slog.Logger) (*Controller, error) {
	if debounce <= 0 {
		return nil, fmt.Errorf("debounce duration must be positive")
	}
	if policyNamespace == "" {
		return nil, fmt.Errorf("policy namespace must be non-empty")
	}
	controller := &Controller{
		serviceInformer: serviceInformer,
		policyInformer:  policyInformer,
		policyNamespace: policyNamespace,
		output:          output,
		debounce:        debounce,
		logger:          logger,
		trigger:         make(chan struct{}, 1),
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { controller.enqueue() },
		UpdateFunc: func(any, any) { controller.enqueue() },
		DeleteFunc: func(any) { controller.enqueue() },
	}
	if _, err := serviceInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("register Service event handler: %w", err)
	}
	if _, err := policyInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("register policy Secret event handler: %w", err)
	}
	return controller, nil
}

func (c *Controller) Ready() bool {
	return c.ready.Load()
}

func (c *Controller) Run(ctx context.Context) error {
	go c.serviceInformer.Run(ctx.Done())
	go c.policyInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), c.serviceInformer.HasSynced, c.policyInformer.HasSynced) {
		return fmt.Errorf("Service and policy Secret informer caches did not synchronize")
	}
	if err := c.reconcile(); err != nil {
		return err
	}
	c.ready.Store(true)
	c.logger.Info("controller ready", "output", c.output, "policy_namespace", c.policyNamespace)

	var timer *time.Timer
	var timerChannel <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case <-c.trigger:
			if timer == nil {
				timer = time.NewTimer(c.debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.debounce)
			}
			timerChannel = timer.C
		case <-timerChannel:
			timerChannel = nil
			if err := c.reconcile(); err != nil {
				c.logger.Error("reconciliation failed", "error", err)
				timer.Reset(c.debounce)
				timerChannel = timer.C
			}
		}
	}
}

func (c *Controller) enqueue() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Controller) reconcile() error {
	serviceObjects := c.serviceInformer.GetStore().List()
	services := make([]*corev1.Service, 0, len(serviceObjects))
	for _, object := range serviceObjects {
		service, ok := object.(*corev1.Service)
		if !ok {
			return fmt.Errorf("Service informer store contained %T, expected *v1.Service", object)
		}
		services = append(services, service.DeepCopy())
	}
	policyObjects := c.policyInformer.GetStore().List()
	policySecrets := make([]*corev1.Secret, 0, len(policyObjects))
	for _, object := range policyObjects {
		secret, ok := object.(*corev1.Secret)
		if !ok {
			return fmt.Errorf("policy informer store contained %T, expected *v1.Secret", object)
		}
		policySecrets = append(policySecrets, secret.DeepCopy())
	}

	generated, serviceErrors, policyErrors := blueprint.Build(services, policySecrets)
	for _, policyError := range policyErrors {
		c.logger.Warn("invalid policy Secret",
			"component", "policy",
			"namespace", policyError.Namespace,
			"policy", policyError.Name,
			"reason", policyError.Err.Error(),
		)
	}
	for _, serviceError := range serviceErrors {
		if serviceError.ResourceID != "" {
			c.logger.Warn("blueprint resource ID collision",
				"component", "blueprint",
				"namespace", serviceError.Namespace,
				"service", serviceError.Name,
				"resource_id", serviceError.ResourceID,
				"conflicts_with", serviceError.ConflictsWith,
			)
			continue
		}
		arguments := []any{
			"component", "blueprint",
			"namespace", serviceError.Namespace,
			"service", serviceError.Name,
			"reason", serviceError.Err.Error(),
		}
		if serviceError.Policy != "" {
			arguments = append(arguments, "policy", serviceError.Policy, "policy_namespace", c.policyNamespace)
		}
		c.logger.Warn("skipping invalid Service", arguments...)
	}
	data, err := blueprint.Marshal(generated)
	if err != nil {
		return err
	}
	changed, err := atomicfile.Write(c.output, data, os.FileMode(0o644))
	if err != nil {
		return err
	}
	if changed {
		c.logger.Info("blueprint updated", "component", "blueprint", "resources", len(generated.PublicResources), "policies", len(generated.PublicPolicies), "invalid_services", len(serviceErrors), "invalid_policies", len(policyErrors), "bytes", len(data))
	} else {
		c.logger.Debug("blueprint unchanged", "component", "blueprint", "resources", len(generated.PublicResources), "policies", len(generated.PublicPolicies), "invalid_services", len(serviceErrors), "invalid_policies", len(policyErrors))
	}
	return nil
}
