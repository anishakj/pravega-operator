/**
 * Copyright (c) 2019 Dell Inc., or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package webhook

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"log"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"

	pravegav1alpha1 "github.com/pravega/pravega-operator/pkg/apis/pravega/v1alpha1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
)

const (
	CertDir = "/tmp"
)

// AddToManagerFuncs is a list of functions to add all Webhooks to the Manager
var AddToManagerFuncs []func(manager.Manager) error

func init() {
	// AddToManagerFuncs is a list of functions to create Webhooks and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, Add)
}

// AddToManager adds all Webhooks to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}
	return nil
}

// Create webhook server and register webhook to it
func Add(mgr manager.Manager) error {
	log.Printf("Initializing webhook")
	svr, err := newWebhookServer(mgr)
	if err != nil {
		log.Printf("Failed to create webhook server: %v", err)
		return err
	}

	wh, err := newValidatingWebhook(mgr)
	if err != nil {
		log.Printf("Failed to create validating webhook: %v", err)
		return err
	}

	svr.Register(wh)
	err = createWebhookK8sService(mgr)
	if err != nil {
		log.Printf("Failed to create webhook svc: %v", err)
	}
	return nil
}

func newValidatingWebhook(mgr manager.Manager) (*admission.Webhook, error) {
	return builder.NewWebhookBuilder().
		Mutating().
		Operations(admissionregistrationv1beta1.Create, admissionregistrationv1beta1.Update).
		ForType(&pravegav1alpha1.PravegaCluster{}).
		Handlers(&pravegaWebhookHandler{}).
		WithManager(mgr).
		Build()
}

func newWebhookServer(mgr manager.Manager) (*webhook.Server, error) {
	return webhook.NewServer("pravega-admission-webhook", mgr, webhook.ServerOptions{
		CertDir: CertDir,
	})
}

func createWebhookK8sService(mgr manager.Manager) error {
	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatal(err)
	}
	c, _ := client.New(cfg, client.Options{Scheme: mgr.GetScheme()})
	// create webhook k8s service
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pravega-admission-webhook",
			Namespace: os.Getenv("WATCH_NAMESPACE"),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"component": "pravega-operator",
			},
			Ports: []corev1.ServicePort{
				{
					// When using service, kube-apiserver will send admission request to port 443.
					Port:       443,
					TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 443},
				},
			},
		},
	}
	// get operator deployment
	nn := types.NamespacedName{Namespace: os.Getenv("WATCH_NAMESPACE"), Name: os.Getenv("OPERATOR_NAME")}
	deployment := &appsv1.Deployment{}
	err = c.Get(context.TODO(), nn, deployment)
	if err != nil {
		return fmt.Errorf("failed to get operator deployment: %v", err)
	}
	// add owner reference
	controllerutil.SetControllerReference(deployment, svc, mgr.GetScheme())
	err = c.Create(context.TODO(), svc)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create webhook k8s service: %v", err)
	}
	return nil
}
