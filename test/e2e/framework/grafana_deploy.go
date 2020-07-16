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

	"github.com/appscode/go/types"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	meta_util "kmodules.xyz/client-go/meta"
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
			Replicas: types.Int32P(1),
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
							Image: "grafana/grafana",
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

	_, err := f.kubeClient.AppsV1().Deployments(obj.Namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
	return err
}

func (f *Framework) DeleteGrafanaDeployment() error {
	return f.kubeClient.AppsV1().Deployments(f.namespace).Delete(context.TODO(), f.name, meta_util.DeleteInForeground())
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
					Port:       3000,
					TargetPort: intstr.IntOrString{IntVal: 3000},
				},
			},
		},
	}

	_, err := f.kubeClient.CoreV1().Services(obj.Namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
	return err
}

func (f *Framework) DeleteGrafanaService() error {
	return f.kubeClient.CoreV1().Services(f.namespace).Delete(context.TODO(), f.name, meta_util.DeleteInForeground())
}
