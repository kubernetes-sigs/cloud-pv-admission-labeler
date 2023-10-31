package admission

import (
	"context"
	"reflect"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
)

type fakePVLabeler struct {
	labels map[string]string
	err    error
}

func (f *fakePVLabeler) GetLabelsForVolume(ctx context.Context, pv *corev1.PersistentVolume) (map[string]string, error) {
	return f.labels, f.err
}

func Test_getVolumeLabels(t *testing.T) {
	testcases := []struct {
		name           string
		providerLabels map[string]string
		providerErr    error
		pv             *corev1.PersistentVolume
		expectedLabels map[string]string
		expectedErr    error
	}{
		{
			name: "Dynamically created PV already has region and zone labels",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Annotations: map[string]string{
						"pv.kubernetes.io/provisioned-by": "gce",
					},
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone1",
						corev1.LabelTopologyRegion: "region1",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
				},
			},
			expectedLabels: map[string]string{
				corev1.LabelTopologyZone:   "zone1",
				corev1.LabelTopologyRegion: "region1",
			},
			expectedErr: nil,
		},
		{
			name: "PV region/zone labels from cloud provider",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
				},
			},
			providerLabels: map[string]string{
				corev1.LabelTopologyZone:   "zone1",
				corev1.LabelTopologyRegion: "region1",
			},
			expectedLabels: map[string]string{
				corev1.LabelTopologyZone:   "zone1",
				corev1.LabelTopologyRegion: "region1",
			},
			expectedErr: nil,
		},
		{
			name: "PV not of type GCE, AWS, Azure or vSphere",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/etc/kubernetes",
						},
					},
				},
			},
			providerLabels: map[string]string{
				corev1.LabelTopologyZone:   "zone1",
				corev1.LabelTopologyRegion: "region1",
			},
			expectedLabels: nil,
			expectedErr:    nil,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			pvLabeler := &fakePVLabeler{
				labels: testcase.providerLabels,
				err:    testcase.providerErr,
			}
			scheme := runtime.NewScheme()
			if err := kubescheme.AddToScheme(scheme); err != nil {
				klog.Fatalf("error adding core Kubernetes types to scheme: %v", err)
			}

			admission := NewPVLabelAdmission("gce", scheme, pvLabeler)
			labels, err := admission.getVolumeLabels(testcase.pv)
			if err != testcase.expectedErr {
				t.Errorf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(labels, testcase.expectedLabels) {
				t.Logf("actual labels: %v", labels)
				t.Logf("expected labels: %v", testcase.expectedLabels)
				t.Error("unexpected labels from getVolumeLabels")
			}
		})
	}
}

func Test_mutatePV(t *testing.T) {
	testcases := []struct {
		name        string
		pv          *corev1.PersistentVolume
		labels      map[string]string
		expectedPV  *corev1.PersistentVolume
		expectedErr error
	}{
		{
			name: "PV region/zone labels from cloud provider",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
				},
			},
			labels: map[string]string{
				corev1.LabelTopologyZone:   "zone1",
				corev1.LabelTopologyRegion: "region1",
			},
			expectedPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone1",
						corev1.LabelTopologyRegion: "region1",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      corev1.LabelTopologyRegion,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"region1"},
										},
										{
											Key:      corev1.LabelTopologyZone,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"zone1"},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "PV existing labels are different",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone1",
						corev1.LabelTopologyRegion: "region1",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
				},
			},
			labels: map[string]string{
				corev1.LabelTopologyZone:   "zone2",
				corev1.LabelTopologyRegion: "region2",
			},
			expectedPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone2",
						corev1.LabelTopologyRegion: "region2",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      corev1.LabelTopologyRegion,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"region2"},
										},
										{
											Key:      corev1.LabelTopologyZone,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"zone2"},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "PV has labels, no labels from cloud provider",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone1",
						corev1.LabelTopologyRegion: "region1",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
				},
			},
			expectedPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gcepd",
					Namespace: "myns",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone1",
						corev1.LabelTopologyRegion: "region1",
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						GCEPersistentDisk: &corev1.GCEPersistentDiskVolumeSource{
							PDName: "123",
						},
					},
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: make([]corev1.NodeSelectorTerm, 1),
						},
					},
				},
			},
			expectedErr: nil,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := kubescheme.AddToScheme(scheme); err != nil {
				klog.Fatalf("error adding core Kubernetes types to scheme: %v", err)
			}
			admission := NewPVLabelAdmission("gce", scheme, nil)

			pv := testcase.pv.DeepCopy()
			err := admission.mutatePV(pv, testcase.labels)
			if err != testcase.expectedErr {
				t.Errorf("unexpected error: %v", err)
			}

			sortMatchExpressions(pv)
			if !reflect.DeepEqual(pv, testcase.expectedPV) {
				t.Logf("actual PV: %v", pv)
				t.Logf("expected PV: %v", testcase.expectedPV)
				t.Error("unexpected PersistentVolume")
			}
		})
	}
}

// sortMatchExpressions sorts a PV's node selector match expressions by key name if it is not nil
func sortMatchExpressions(pv *corev1.PersistentVolume) {
	if pv.Spec.NodeAffinity == nil ||
		pv.Spec.NodeAffinity.Required == nil ||
		pv.Spec.NodeAffinity.Required.NodeSelectorTerms == nil {
		return
	}

	match := pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions
	sort.Slice(match, func(i, j int) bool {
		return match[i].Key < match[j].Key
	})

	pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions = match
}
