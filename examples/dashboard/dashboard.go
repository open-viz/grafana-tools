/*
Copyright AppsCode Inc. and Contributors

Licensed under the AppsCode Free Trial License 1.0.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://github.com/appscode/licenses/raw/1.0.0/AppsCode-Free-Trial-1.0.0.md

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	api "go.openviz.dev/grafana-operator/apis/openviz/v1alpha1"
	crd_client "go.openviz.dev/grafana-operator/client/clientset/versioned/typed/openviz/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/retry"
)

var kubeconfig *string

const (
	ns              = "demo"
	SampleDashboard = "sample-dashboard"
)

func init() {
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
}

func CreateDashboard(model runtime.RawExtension) error {
	dBoard := &api.Dashboard{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SampleDashboard,
			Namespace: ns,
		},
		Spec: api.DashboardSpec{
			Grafana: &api.TargetRef{
				Name: "grafana-apb",
			},
			Model:     &model,
			FolderID:  0,
			Overwrite: true,
			Templatize: &api.ModelTemplateConfiguration{
				Title:      true,
				Datasource: true,
			},
		},
	}
	gClient, err := createClient()
	if err != nil {
		return err
	}
	_, err = gClient.Dashboards(ns).Create(context.Background(), dBoard, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	time.Sleep(5 * time.Second)
	createdBoard, err := gClient.Dashboards(ns).Get(context.Background(), dBoard.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	fmt.Println(createdBoard.Status)
	return nil
}

func DeleteDashboard(name string) error {
	gClient, err := createClient()
	if err != nil {
		return err
	}
	err = gClient.Dashboards(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

func GetDashboard(name string) (*api.Dashboard, error) {
	gClient, err := createClient()
	if err != nil {
		return nil, err
	}
	dsBoard, err := gClient.Dashboards(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return dsBoard, nil
}

func UpdateDashboard(name string, model runtime.RawExtension) error {
	gClient, err := createClient()
	if err != nil {
		return err
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		res, getErr := gClient.Dashboards(ns).Get(context.Background(), name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		res.Spec.Model = &model
		_, updateErr := gClient.Dashboards(ns).Update(context.Background(), res, metav1.UpdateOptions{})
		return updateErr
	})
	return retryErr
}

func main() {
	model, err := ioutil.ReadFile("model.json")
	if err != nil {
		panic(err)
	}
	updatedModel, err := ioutil.ReadFile("updatedModel.json")
	if err != nil {
		panic(err)
	}

	fmt.Println("================== Creating Dashboard ===================")
	err = CreateDashboard(runtime.RawExtension{Raw: model})
	if err != nil {
		panic(err)
	}
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("================= Updating dashboard ===================")
	err = UpdateDashboard(SampleDashboard, runtime.RawExtension{Raw: updatedModel})
	if err != nil {
		panic(err)
	}
	fmt.Println("Successfully updated dashboard")
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("================== Get Dashboard after updating ===================")
	dsBoard, err := GetDashboard(SampleDashboard)
	if err != nil {
		panic(err)
	}
	fmt.Println(dsBoard.Status)
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("================= Deleting dashboard ==================")
	err = DeleteDashboard("sample-dashboard")
	if err != nil {
		panic(err)
	}
	fmt.Println("Successfully deleted dashboard")
}

func createClient() (*crd_client.OpenvizV1alpha1Client, error) {
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err)
	}
	gClient, err := crd_client.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	return gClient, nil
}
