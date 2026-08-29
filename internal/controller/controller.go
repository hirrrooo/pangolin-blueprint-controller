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
	informer cache.SharedIndexInformer
	output   string
	debounce time.Duration
	logger   *slog.Logger
	trigger  chan struct{}
	ready    atomic.Bool
}

func New(informer cache.SharedIndexInformer, output string, debounce time.Duration, logger *slog.Logger) (*Controller, error) {
	if debounce <= 0 {
		return nil, fmt.Errorf("debounce duration must be positive")
	}
	controller := &Controller{
		informer: informer,
		output:   output,
		debounce: debounce,
		logger:   logger,
		trigger:  make(chan struct{}, 1),
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { controller.enqueue() },
		UpdateFunc: func(any, any) { controller.enqueue() },
		DeleteFunc: func(any) { controller.enqueue() },
	})
	if err != nil {
		return nil, fmt.Errorf("register Service event handler: %w", err)
	}
	return controller, nil
}

func (c *Controller) Ready() bool {
	return c.ready.Load()
}

func (c *Controller) Run(ctx context.Context) error {
	go c.informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), c.informer.HasSynced) {
		return fmt.Errorf("Service informer cache did not synchronize")
	}
	if err := c.reconcile(); err != nil {
		return err
	}
	c.ready.Store(true)
	c.logger.Info("controller ready", "output", c.output)

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
	objects := c.informer.GetStore().List()
	services := make([]*corev1.Service, 0, len(objects))
	for _, object := range objects {
		service, ok := object.(*corev1.Service)
		if !ok {
			return fmt.Errorf("informer store contained %T, expected *v1.Service", object)
		}
		services = append(services, service.DeepCopy())
	}

	generated, serviceErrors := blueprint.Build(services)
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
		c.logger.Warn("skipping invalid Service",
			"component", "blueprint",
			"namespace", serviceError.Namespace,
			"service", serviceError.Name,
			"reason", serviceError.Err.Error(),
		)
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
		c.logger.Info("blueprint updated", "component", "blueprint", "resources", len(generated.PublicResources), "invalid_services", len(serviceErrors), "bytes", len(data))
	} else {
		c.logger.Debug("blueprint unchanged", "component", "blueprint", "resources", len(generated.PublicResources), "invalid_services", len(serviceErrors))
	}
	return nil
}
