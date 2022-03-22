/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	grafana_sdk "go.openviz.dev/grafana-sdk"

	. "github.com/onsi/gomega"
	"gomodules.xyz/pointer"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	grafanaServicePort = 3000
)

func (f *Framework) GrafanaDeploymentName() string {
	return f.name
}

func (f *Framework) CreateGrafanaDeployment() error {
	obj := &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Spec: apps.DeploymentSpec{
			Replicas: pointer.Int32P(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": f.name,
				},
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      f.name,
					Namespace: f.namespace,
					Labels: map[string]string{
						"app": f.name,
					},
				},
				Spec: core.PodSpec{
					Volumes: []core.Volume{
						{
							Name: "grafana-storage",
							VolumeSource: core.VolumeSource{
								EmptyDir: &core.EmptyDirVolumeSource{},
							},
						},
					},
					Containers: []core.Container{
						{
							Name:  f.name,
							Image: "grafana/grafana:7.5.11",
							Ports: []core.ContainerPort{
								{
									Name:          "grafana",
									ContainerPort: 3000,
								},
							},
							Resources: core.ResourceRequirements{},
							VolumeMounts: []core.VolumeMount{
								{
									Name:      "grafana-storage",
									MountPath: "/var/lib/grafana",
								},
							},
							ImagePullPolicy: core.PullIfNotPresent,
						},
					},
				},
			},
		},
	}

	return f.cc.Create(context.TODO(), obj)
}

func (f *Framework) DeleteGrafanaDeployment() error {
	return f.cc.Delete(context.TODO(), &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
	})
}

func (f *Framework) GrafanaServiceName() string {
	return f.name
}

func (f *Framework) CreateGrafanaService() error {
	obj := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Spec: core.ServiceSpec{
			Selector: map[string]string{
				"app": f.name,
			},
			Type: core.ServiceTypeClusterIP,
			Ports: []core.ServicePort{
				{
					Port:       grafanaServicePort,
					TargetPort: intstr.IntOrString{IntVal: 3000},
				},
			},
		},
	}

	return f.cc.Create(context.TODO(), obj)
}

func (f *Framework) DeleteGrafanaService() error {
	return f.cc.Delete(context.TODO(), &core.Service{ObjectMeta: metav1.ObjectMeta{Name: f.name, Namespace: f.namespace}})
}

func (f *Framework) isGrafanaReady() bool {
	url := fmt.Sprintf("http://%s.%s.svc:%d/api/health", f.name, f.namespace, grafanaServicePort)
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	gHealth := &grafana_sdk.HealthResponse{}

	err = json.NewDecoder(resp.Body).Decode(gHealth)
	Expect(err).ShouldNot(HaveOccurred())

	return resp.StatusCode == http.StatusOK && gHealth.Database == "ok" // Ref: https://grafana.com/docs/grafana/latest/http_api/other/#health-api
}
