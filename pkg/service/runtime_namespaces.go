package service

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func resolveRuntimeInstanceNamespace(namespace string) (string, error) {
	trimmed := strings.TrimSpace(namespace)
	if trimmed == "" || trimmed == runtimev1alpha1.DefaultInstanceNamespace {
		return runtimev1alpha1.DefaultInstanceNamespace, nil
	}
	return "", &ValidationError{
		Message: fmt.Sprintf("namespace is fixed to %q for runtime resources", runtimev1alpha1.DefaultInstanceNamespace),
	}
}

func normalizeRuntimeResourceName(name string) (string, error) {
	trimmedName := strings.ToLower(strings.TrimSpace(name))
	if trimmedName == "" {
		return "", &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(trimmedName); len(errs) > 0 {
		return "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", trimmedName, strings.Join(errs, ", ")),
		}
	}
	return trimmedName, nil
}
