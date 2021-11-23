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

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/grafana-tools/sdk"
	cs "go.openviz.dev/grafana-tools/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	GrafanaHost = "https://grafana.byte.builders"
)

var kubeconfig *string

func init() {
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
}

func GenerateEmbeddedURLs(c cs.Interface, db metav1.ObjectMeta) ([]string, error) {
	dashboard, err := c.OpenvizV1alpha1().Dashboards(db.Namespace).Get(context.TODO(), db.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	board := &sdk.Board{}
	if dashboard.Spec.Model != nil {
		err = json.Unmarshal(dashboard.Spec.Model.Raw, board)
		if err != nil {
			return nil, err
		}
	}

	urls := make([]string, 0, len(board.Panels))
	for _, p := range board.Panels {
		// url template: "http://{{.URL}}/d-solo/{{.BoardUID}}/{{.DashboardName}}?orgId={{.OrgID}}&from={{.From}}&to={{.To}}&theme={{.Theme}}&panelId="

		url := fmt.Sprintf("%v/d-solo/%v/%v?orgId=%v&from=%v&to=%v&panelId=%v", GrafanaHost, board.UID, *dashboard.Status.Dashboard.Slug, *dashboard.Status.Dashboard.OrgID, time.Now().Unix(), time.Now().Unix(), p.ID)
		urls = append(urls, url)
	}
	return urls, nil
}

func main() {
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err)
	}
	client, err := cs.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	db := metav1.ObjectMeta{
		Name:      "sample-dashboard",
		Namespace: "demo",
	}
	urls, err := GenerateEmbeddedURLs(client, db)
	if err != nil {
		panic(err)
	}
	for _, u := range urls {
		fmt.Println(u)
	}
}
