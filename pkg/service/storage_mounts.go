package service

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// normalizeGPUUnitStorageMounts validates the runtime storage attachment slice.
func normalizeGPUUnitStorageMounts(mounts []runtimev1alpha1.GPUUnitStorageMount) ([]runtimev1alpha1.GPUUnitStorageMount, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	seenNames := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	out := make([]runtimev1alpha1.GPUUnitStorageMount, 0, len(mounts))

	for _, mount := range mounts {
		mount.Name = strings.ToLower(strings.TrimSpace(mount.Name))
		if mount.Name == "" {
			return nil, &ValidationError{Message: "storageMounts.name is required"}
		}
		if errs := validation.IsDNS1123Subdomain(mount.Name); len(errs) > 0 {
			return nil, &ValidationError{
				Message: fmt.Sprintf("storageMounts.name %q is invalid: %s", mount.Name, strings.Join(errs, ", ")),
			}
		}
		if _, exists := seenNames[mount.Name]; exists {
			return nil, &ValidationError{Message: fmt.Sprintf("storageMounts.name %q is duplicated", mount.Name)}
		}
		seenNames[mount.Name] = struct{}{}

		mount.MountPath = strings.TrimSpace(mount.MountPath)
		if mount.MountPath == "" {
			return nil, &ValidationError{Message: fmt.Sprintf("storageMounts[%s].mountPath is required", mount.Name)}
		}
		if !path.IsAbs(mount.MountPath) {
			return nil, &ValidationError{
				Message: fmt.Sprintf("storageMounts[%s].mountPath %q must be an absolute path", mount.Name, mount.MountPath),
			}
		}
		mount.MountPath = path.Clean(mount.MountPath)
		if mount.MountPath == "/" {
			return nil, &ValidationError{
				Message: fmt.Sprintf("storageMounts[%s].mountPath cannot be /", mount.Name),
			}
		}
		if _, exists := seenPaths[mount.MountPath]; exists {
			return nil, &ValidationError{Message: fmt.Sprintf("storageMounts.mountPath %q is duplicated", mount.MountPath)}
		}
		seenPaths[mount.MountPath] = struct{}{}

		out = append(out, mount)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].MountPath < out[j].MountPath
	})

	return out, nil
}
