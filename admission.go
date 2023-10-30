package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wI2L/jsondiff"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	jsonserializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	cloudprovider "k8s.io/cloud-provider"
	volumehelpers "k8s.io/cloud-provider/volume/helpers"
	storagehelpers "k8s.io/component-helpers/storage/volume"
	"k8s.io/klog/v2"
)

type pvLabelAdmission struct {
	scheme *runtime.Scheme

	cloudProvider string
	pvLabeler     cloudprovider.PVLabeler
}

func (p *pvLabelAdmission) admit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	data, err := io.ReadAll(r.Body)
	if err != nil {
		klog.ErrorS(err, "failed to read request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	codec := jsonserializer.NewSerializerWithOptions(jsonserializer.DefaultMetaFactory, p.scheme, p.scheme, jsonserializer.SerializerOptions{})
	obj, _, err := codec.Decode(data, nil, nil)
	if err != nil {
		klog.ErrorS(err, "failed to decode request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	admissionReview, ok := obj.(*admissionv1.AdmissionReview)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if admissionReview.Request.Kind.Kind != "PersistentVolume" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pv := &corev1.PersistentVolume{}
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, pv); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	volumeLabels, err := p.getVolumeLabels(pv)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if len(volumeLabels) == 0 {
		// write allowed
		return
	}

	oldPV := pv.DeepCopy()
	newPV := pv.DeepCopy()
	err = p.mutatePV(newPV, volumeLabels)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	patchBytes, err := p.getPatchBytes(oldPV, newPV)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	patchType := admissionv1.PatchTypeJSONPatch

	resp := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:       admissionReview.Request.UID,
			Allowed:   true,
			PatchType: &patchType,
			Patch:     patchBytes,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	outBytes, err := json.Marshal(resp)
	if err != nil {
		e := fmt.Sprintf("could not parse admission response: %v", err)
		http.Error(w, e, http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "%s", outBytes)
}

func (p *pvLabelAdmission) getPatchBytes(oldPV, newPV *corev1.PersistentVolume) ([]byte, error) {
	patch, err := jsondiff.Compare(oldPV, newPV)
	if err != nil {
		return nil, err
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}

	return patchBytes, err
}

func (p *pvLabelAdmission) mutatePV(pv *corev1.PersistentVolume, volumeLabels map[string]string) error {
	requirements := make([]corev1.NodeSelectorRequirement, 0)

	if pv.Labels == nil {
		pv.Labels = make(map[string]string)
	}

	for k, v := range volumeLabels {
		// We (silently) replace labels if they are provided.
		// This should be OK because they are in the kubernetes.io namespace
		// i.e. we own them
		pv.Labels[k] = v

		// Set NodeSelectorRequirements based on the labels
		var values []string
		if k == v1.LabelTopologyZone || k == v1.LabelFailureDomainBetaZone {
			zones, err := volumehelpers.LabelZonesToSet(v)
			if err != nil {
				return fmt.Errorf("failed to convert label string for Zone: %s to a Set", v)
			}
			// zone values here are sorted for better testability.
			values = zones.List()
		} else {
			values = []string{v}
		}
		requirements = append(requirements, corev1.NodeSelectorRequirement{Key: k, Operator: corev1.NodeSelectorOpIn, Values: values})
	}

	if pv.Spec.NodeAffinity == nil {
		pv.Spec.NodeAffinity = new(corev1.VolumeNodeAffinity)
	}
	if pv.Spec.NodeAffinity.Required == nil {
		pv.Spec.NodeAffinity.Required = new(corev1.NodeSelector)
	}
	if len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) == 0 {
		// Need at least one term pre-allocated whose MatchExpressions can be appended to
		pv.Spec.NodeAffinity.Required.NodeSelectorTerms = make([]corev1.NodeSelectorTerm, 1)
	}
	if nodeSelectorRequirementKeysExistInNodeSelectorTerms(requirements, pv.Spec.NodeAffinity.Required.NodeSelectorTerms) {
		klog.V(4).Infof("NodeSelectorRequirements for cloud labels %v conflict with existing NodeAffinity %v. Skipping addition of NodeSelectorRequirements for cloud labels.",
			requirements, pv.Spec.NodeAffinity)
	} else {
		for _, req := range requirements {
			for i := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
				pv.Spec.NodeAffinity.Required.NodeSelectorTerms[i].MatchExpressions = append(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[i].MatchExpressions, req)
			}
		}
	}

	return nil
}

func (p *pvLabelAdmission) getVolumeLabels(pv *corev1.PersistentVolume) (map[string]string, error) {
	existingLabels := pv.Labels

	// All cloud providers set only these two labels.
	topologyLabelGA := true
	domain, domainOK := existingLabels[corev1.LabelTopologyZone]
	region, regionOK := existingLabels[corev1.LabelTopologyRegion]
	// If they don't have GA labels we should check for failuredomain beta labels
	// TODO: remove this once all the cloud provider change to GA topology labels
	if !domainOK || !regionOK {
		topologyLabelGA = false
		domain, domainOK = existingLabels[corev1.LabelFailureDomainBetaZone]
		region, regionOK = existingLabels[corev1.LabelFailureDomainBetaRegion]
	}

	isDynamicallyProvisioned := metav1.HasAnnotation(pv.ObjectMeta, storagehelpers.AnnDynamicallyProvisioned)
	if isDynamicallyProvisioned && domainOK && regionOK {
		// PV already has all the labels and we can trust the dynamic provisioning that it provided correct values.
		if topologyLabelGA {
			return map[string]string{
				v1.LabelTopologyZone:   domain,
				v1.LabelTopologyRegion: region,
			}, nil
		}
		return map[string]string{
			v1.LabelFailureDomainBetaZone:   domain,
			v1.LabelFailureDomainBetaRegion: region,
		}, nil

	}

	switch {
	case p.cloudProvider == "gce" && pv.Spec.GCEPersistentDisk != nil:
		labels, err := p.pvLabeler.GetLabelsForVolume(context.Background(), pv)
		if err != nil {
			return nil, fmt.Errorf("error querying GCE PD volume %s: %v", pv.Spec.GCEPersistentDisk.PDName, err)
		}
		return labels, nil
	case p.cloudProvider == "azure" && pv.Spec.AzureDisk != nil:
		labels, err := p.pvLabeler.GetLabelsForVolume(context.Background(), pv)
		if err != nil {
			return nil, fmt.Errorf("error querying AzureDisk volume %s: %v", pv.Spec.AzureDisk.DiskName, err)
		}
		return labels, nil
	case p.cloudProvider == "aws" && pv.Spec.AWSElasticBlockStore != nil:
		labels, err := p.pvLabeler.GetLabelsForVolume(context.Background(), pv)
		if err != nil {
			return nil, fmt.Errorf("error querying AWS EBS Volume %s: %v", pv.Spec.AWSElasticBlockStore.VolumeID, err)
		}
		return labels, nil
	case p.cloudProvider == "vsphere" && pv.Spec.VsphereVolume != nil:
		labels, err := p.pvLabeler.GetLabelsForVolume(context.Background(), pv)
		if err != nil {
			return nil, fmt.Errorf("error querying vSphere Volume %s: %v", pv.Spec.VsphereVolume.VolumePath, err)
		}
		return labels, nil
	}

	// Unrecognized volume, do not add any labels
	return nil, nil
}

func nodeSelectorRequirementKeysExistInNodeSelectorTerms(reqs []corev1.NodeSelectorRequirement, terms []corev1.NodeSelectorTerm) bool {
	for _, req := range reqs {
		for _, term := range terms {
			for _, r := range term.MatchExpressions {
				if r.Key == req.Key {
					return true
				}
			}
		}
	}
	return false
}
