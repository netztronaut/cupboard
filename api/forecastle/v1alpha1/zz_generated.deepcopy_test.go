package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestForecastleAppDeepCopy(t *testing.T) {
	original := &ForecastleApp{
		ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
		Spec: ForecastleAppSpec{
			Name:  "Test App",
			URL:   "https://example.com",
			Group: "test-group",
		},
	}

	copied := original.DeepCopyObject()
	if copied == nil {
		t.Fatal("copy should not be nil")
	}

	copiedApp, ok := copied.(*ForecastleApp)
	if !ok {
		t.Fatalf("copy should be *ForecastleApp, got %T", copied)
	}

	if copiedApp.Name != original.Name {
		t.Errorf("expected name %s, got %s", original.Name, copiedApp.Name)
	}
}

func TestForecastleAppListDeepCopy(t *testing.T) {
	original := &ForecastleAppList{
		Items: []ForecastleApp{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-1", Namespace: "default"},
				Spec:       ForecastleAppSpec{Name: "app1", URL: "https://a.com", Group: "g1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-2", Namespace: "default"},
				Spec:       ForecastleAppSpec{Name: "app2", URL: "https://b.com", Group: "g1"},
			},
		},
	}

	copied := original.DeepCopyObject()
	if copied == nil {
		t.Fatal("copy should not be nil")
	}

	copiedList, ok := copied.(*ForecastleAppList)
	if !ok {
		t.Fatalf("copy should be *ForecastleAppList, got %T", copied)
	}

	if len(copiedList.Items) != len(original.Items) {
		t.Errorf("expected %d items, got %d", len(original.Items), len(copiedList.Items))
	}
}
