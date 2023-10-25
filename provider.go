package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	cloudprovider "k8s.io/cloud-provider"
	_ "k8s.io/cloud-provider-aws/pkg/providers/v1"
	_ "k8s.io/legacy-cloud-providers/azure"
	_ "k8s.io/legacy-cloud-providers/gce"
	_ "k8s.io/legacy-cloud-providers/vsphere"
)

func newProvider(cloudProviderName, cloudConfigPath string) (cloudprovider.PVLabeler, error) {
	cloudConfig, err := os.ReadFile(cloudConfigPath)
	if err != nil {
		return nil, fmt.Errorf("error reading cloud config file %s: %v", cloudConfigPath, err)
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
