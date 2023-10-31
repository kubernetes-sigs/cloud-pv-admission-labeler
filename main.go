package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"

	_ "k8s.io/cloud-provider-aws/pkg/providers/v1"
	_ "k8s.io/legacy-cloud-providers/azure"
	_ "k8s.io/legacy-cloud-providers/gce"
	_ "k8s.io/legacy-cloud-providers/vsphere"

	"sigs.k8s.io/cloud-pv-admission-labeler/admission"
)

var (
	addr            string
	tlsCertPath     string
	tlsKeyPath      string
	cloudProvider   string
	cloudConfigPath string
)

func main() {
	flag.StringVar(&addr, "addr", ":9001", "listen address of the server")
	flag.StringVar(&tlsCertPath, "tls-cert-path", "", "the path to the serving certificate")
	flag.StringVar(&tlsKeyPath, "tls-key-path", "", "the path to the serving key")
	flag.StringVar(&cloudProvider, "cloud-provider", "", "the cloud provider implementation")
	flag.StringVar(&cloudConfigPath, "cloud-config", "", "the path to the cloud config")
	flag.Parse()

	scheme := runtime.NewScheme()
	if err := kscheme.AddToScheme(scheme); err != nil {
		klog.Fatalf("error adding core Kubernetes types to scheme: %v", err)
	}

	if err := admissionv1.AddToScheme(scheme); err != nil {
		klog.Fatalf("error adding admission/v1 types to scheme: %v", err)
	}

	pvLabeler, err := newProvider(cloudProvider, cloudConfigPath)
	if err != nil {
		klog.Fatalf("error initializing cloud provider: %v", err)
	}

	pvLabelAdmission := admission.NewPVLabelAdmission(cloudProvider, scheme, pvLabeler)

	mux := http.NewServeMux()
	mux.HandleFunc("/admit", pvLabelAdmission.Admit)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
		},
	}

	klog.Info("Starting webhook server")
	log.Fatal(server.ListenAndServeTLS(tlsCertPath, tlsKeyPath))
}

func newProvider(cloudProviderName, cloudConfigPath string) (cloudprovider.PVLabeler, error) {
	var err error
	var cloudConfig []byte
	if cloudConfigPath != "" {
		cloudConfig, err = os.ReadFile(cloudConfigPath)
		if err != nil {
			return nil, fmt.Errorf("error reading cloud config file %s: %v", cloudConfigPath, err)
		}
	}

	var cloudConfigReader io.Reader
	if len(cloudConfig) > 0 {
		cloudConfigReader = bytes.NewReader(cloudConfig)
	}

	cloudProvider, err := cloudprovider.GetCloudProvider(cloudProviderName, cloudConfigReader)
	if err != nil || cloudProvider == nil {
		return nil, err
	}

	pVLabeler, ok := cloudProvider.(cloudprovider.PVLabeler)
	if !ok {
		return nil, errors.New("cloud provider does not implement PV labeling")
	}

	return pVLabeler, nil
}
