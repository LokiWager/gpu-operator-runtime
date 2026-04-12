package service

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func normalizeGPUStorageName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", name, strings.Join(errs, ", ")),
		}
	}
	return name, nil
}

func normalizeGPUStorageSize(size string) (string, resource.Quantity, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return "", resource.Quantity{}, &ValidationError{Message: "size is required"}
	}

	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return "", resource.Quantity{}, &ValidationError{
			Message: fmt.Sprintf("size %q is invalid: %v", size, err),
		}
	}
	return qty.String(), qty, nil
}

func normalizeGPUStorageClassName(storageClassName string) (string, error) {
	storageClassName = strings.TrimSpace(storageClassName)
	if storageClassName == "" {
		storageClassName = runtimev1alpha1.DefaultGPUStorageClassName
	}
	if errs := validation.IsDNS1123Subdomain(storageClassName); len(errs) > 0 {
		return "", &ValidationError{
			Message: fmt.Sprintf("storageClassName %q is invalid: %s", storageClassName, strings.Join(errs, ", ")),
		}
	}
	return storageClassName, nil
}

// normalizeGPUStoragePrepare validates the storage prepare contract before it reaches the controller.
func normalizeGPUStoragePrepare(prepare runtimev1alpha1.GPUStoragePrepareSpec) (runtimev1alpha1.GPUStoragePrepareSpec, error) {
	prepare.FromImage = strings.TrimSpace(prepare.FromImage)
	prepare.FromStorageName = strings.ToLower(strings.TrimSpace(prepare.FromStorageName))

	command := make([]string, 0, len(prepare.Command))
	for _, part := range prepare.Command {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			command = append(command, trimmed)
		}
	}
	prepare.Command = command

	args := make([]string, 0, len(prepare.Args))
	for _, part := range prepare.Args {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			args = append(args, trimmed)
		}
	}
	prepare.Args = args

	if prepare.FromImage != "" && prepare.FromStorageName != "" {
		return runtimev1alpha1.GPUStoragePrepareSpec{}, &ValidationError{
			Message: "prepare.fromImage and prepare.fromStorageName are mutually exclusive",
		}
	}
	if prepare.FromStorageName != "" {
		if errs := validation.IsDNS1123Subdomain(prepare.FromStorageName); len(errs) > 0 {
			return runtimev1alpha1.GPUStoragePrepareSpec{}, &ValidationError{
				Message: fmt.Sprintf("prepare.fromStorageName %q is invalid: %s", prepare.FromStorageName, strings.Join(errs, ", ")),
			}
		}
		if len(prepare.Command) > 0 || len(prepare.Args) > 0 {
			return runtimev1alpha1.GPUStoragePrepareSpec{}, &ValidationError{
				Message: "prepare.command and prepare.args are not supported when prepare.fromStorageName is set",
			}
		}
	}
	if prepare.FromImage == "" {
		if len(prepare.Command) > 0 || len(prepare.Args) > 0 {
			return runtimev1alpha1.GPUStoragePrepareSpec{}, &ValidationError{
				Message: "prepare.command and prepare.args require prepare.fromImage",
			}
		}
		return prepare, nil
	}
	if len(prepare.Command) == 0 && len(prepare.Args) == 0 {
		return runtimev1alpha1.GPUStoragePrepareSpec{}, &ValidationError{
			Message: "prepare.command or prepare.args is required when prepare.fromImage is set",
		}
	}
	return prepare, nil
}

func isZeroGPUStoragePrepare(prepare runtimev1alpha1.GPUStoragePrepareSpec) bool {
	return prepare.FromImage == "" &&
		prepare.FromStorageName == "" &&
		len(prepare.Command) == 0 &&
		len(prepare.Args) == 0
}

func normalizeGPUStorageAccessor(accessor runtimev1alpha1.GPUStorageAccessorSpec) runtimev1alpha1.GPUStorageAccessorSpec {
	return accessor
}
