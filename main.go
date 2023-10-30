package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
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

	p := &pvLabelAdmission{
		scheme:        scheme,
		cloudProvider: cloudProvider,
		pvLabeler:     pvLabeler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admit", p.admit)
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
