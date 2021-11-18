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
	"path/filepath"
	"time"

	api "go.openviz.dev/grafana-operator/apis/openviz/v1alpha1"
	crd_client "go.openviz.dev/grafana-operator/client/clientset/versioned/typed/openviz/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/retry"
)

var kubeconfig *string

const (
	ns               = "demo"
	SampleDatasource = "sample-ds"
)

func init() {
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
}

func CreateDatasource(url string, sourceType api.DatasourceType, accessType api.DatasourceAccessType) error {
	ds := &api.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SampleDatasource,
			Namespace: "demo",
		},
		Spec: api.DatasourceSpec{
			Grafana: &api.TargetRef{
				Name:       "grafana-apb",
			},
			Name:   "some-random-name",
			Type:   sourceType,
			Access: accessType,
			URL:    url,
			OrgID: 1,
		},
	}
	gClient, err := createClient()
	if err != nil {
		return err
	}
	_, err = gClient.Datasources(ns).Create(context.TODO(), ds, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	time.Sleep(5 * time.Second)
	createdDS, err := gClient.Datasources(ns).Get(context.TODO(), SampleDatasource, metav1.GetOptions{})
	if err != nil {
		return err
	}
	fmt.Println(createdDS)
	return nil
}

func UpdateDatasource(name, url string, sourceType api.DatasourceType, accessType api.DatasourceAccessType) error {
	gClient, err := createClient()
	if err != nil {
		return err
	}
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		res, getErr := gClient.Datasources(ns).Get(context.TODO(), name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		res.Spec.URL = url
		res.Spec.Access = accessType
		res.Spec.Type = sourceType
		_, updateErr := gClient.Datasources(ns).Update(context.Background(), res, metav1.UpdateOptions{})
		return updateErr
	})
	return retryErr
}

func DeleteDatasource(name string) error {
	gClient, err := createClient()
	if err != nil {
		return err
	}
	err = gClient.Datasources(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
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

func GetDatasource(name string) (*api.Datasource, error) {
	gClient, err := createClient()
	if err != nil {
		return nil, err
	}
	ds, err := gClient.Datasources(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return ds, nil
}

func main() {
	fmt.Println("=============== Creating Datasource =================")
	err := CreateDatasource("http://127.0.0.1:9090", api.DatasourceTypePrometheus, api.DatasourceAccessTypeProxy)
	if err != nil {
		panic(err)
	}
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("================ Updating Datasource ===================")
	err = UpdateDatasource(SampleDatasource, "http://127.0.0.1:9099", api.DatasourceTypePrometheus, api.DatasourceAccessTypeProxy)
	if err != nil {
		panic(err)
	}
	fmt.Println("Successfully updated datasource")
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("=============== Get datasource after update ==================")
	ds, err := GetDatasource(SampleDatasource)
	if err != nil {
		panic(err)
	}
	fmt.Println(ds)
	fmt.Println()

	fmt.Println("Press any key to continue")
	fmt.Scanln()
	fmt.Println("================== Deleting Datasource ====================")
	err = DeleteDatasource(SampleDatasource)
	if err != nil {
		panic(err)
	}
	fmt.Println("Datasource successfully deleted")
}
