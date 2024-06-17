/*
Copyright 2016 The Kubernetes Authors.

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
	"flag"
	"fmt"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	ns := os.Getenv("NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	// helm setup
	settings := cli.New()
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
		log.Printf("%+v", err)
		os.Exit(1)
	}
	chartPath := "../testchart"
	chart, err := loader.Load(chartPath)

	if err != nil {
		log.Printf("%+v", err)
		os.Exit(1)
	}

	handledConfigMaps := make(map[string]string)
	for {
		seenConfigMaps := make(map[string]string)
		configMaps, err := clientset.CoreV1().ConfigMaps(ns).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		for _, configMap := range configMaps.Items {
			if strings.ToLower(configMap.Annotations["trigger-install"]) == "true" {
				seenConfigMaps[configMap.Name] = configMap.Name
				fmt.Printf("ConfigMap name: %s\n", configMap.Name)
				if _, exists := handledConfigMaps[configMap.Name]; exists {
					fmt.Printf("We already knows about: %s\n", configMap.Name)
					if handledConfigMaps[configMap.Name] != configMap.Data["values.yaml"] {
						fmt.Printf("We should update: %s\n", configMap.Name)
						values := make(map[string]interface{})
						err := yaml.Unmarshal([]byte(configMap.Data["values.yaml"]), &values)
						if err != nil {
							log.Printf("Values file unmarshall failed with error %s for %s with values %s", err.Error(), configMap.Name, configMap.Data["values.yaml"])
							continue
						}
						upgrade := action.NewUpgrade(actionConfig)
						upgrade.Namespace = ns
						_, err = upgrade.Run(configMap.Name, chart, values)
						if err != nil {
							log.Printf("Upgrade failed with error %s for %s with values %s", err.Error(), configMap.Name, configMap.Data["values.yaml"])
							continue
						}
						handledConfigMaps[configMap.Name] = configMap.Data["values.yaml"]
					}
				} else {
					log.Printf("Untracked CM: %s\n", configMap.Name)
					values := make(map[string]interface{})
					err := yaml.Unmarshal([]byte(configMap.Data["values.yaml"]), &values)
					if err != nil {
						log.Printf("Values file unmarshall failed with error %s for %s with values %s", err.Error(), configMap.Name, configMap.Data["values.yaml"])
						continue
					}
					fmt.Println(values)
					install := action.NewInstall(actionConfig)
					install.Namespace = ns
					install.ReleaseName = configMap.Name
					_, err = install.Run(chart, values)
					if err != nil {
						log.Printf("%s is already installed, trying upgrade instead", configMap.Name)
						handledConfigMaps[configMap.Name] = ""
						continue
					}
					handledConfigMaps[configMap.Name] = configMap.Data["values.yaml"]
				}
			}
		}
		for name := range handledConfigMaps {
			if _, exists := seenConfigMaps[name]; !exists {
				fmt.Printf("CM %s doesn't exist anymore, uninstalling\n", name)
				uninstall := action.NewUninstall(actionConfig)
				_, err := uninstall.Run(name)
				if err != nil {
					log.Printf("couldn't delete %s because of %s", name, err.Error())
					continue
				}
				delete(handledConfigMaps, name)
			}
		}
		time.Sleep(10 * time.Second)
	}
}
