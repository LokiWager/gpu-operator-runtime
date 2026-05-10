package contract

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// CreateGPUUnitRequest asks the service to consume one ready stock unit into active runtime.
type CreateGPUUnitRequest struct {
	OperationID   string                                `json:"operationID"`
	Name          string                                `json:"name"`
	SpecName      string                                `json:"specName"`
	Image         string                                `json:"image"`
	Template      runtimev1alpha1.GPUUnitTemplate       `json:"template,omitempty"`
	Access        runtimev1alpha1.GPUUnitAccess         `json:"access,omitempty"`
	SSH           runtimev1alpha1.GPUUnitSSHSpec        `json:"ssh,omitempty"`
	Serverless    runtimev1alpha1.GPUUnitServerlessSpec `json:"serverless,omitempty"`
	StorageMounts []runtimev1alpha1.GPUUnitStorageMount `json:"storageMounts,omitempty"`
}

// UpdateGPUUnitRequest captures the mutable runtime fields for an active unit.
type UpdateGPUUnitRequest struct {
	Image         string                                 `json:"image,omitempty"`
	Template      runtimev1alpha1.GPUUnitTemplate        `json:"template,omitempty"`
	Access        runtimev1alpha1.GPUUnitAccess          `json:"access,omitempty"`
	SSH           *runtimev1alpha1.GPUUnitSSHSpec        `json:"ssh,omitempty"`
	Serverless    *runtimev1alpha1.GPUUnitServerlessSpec `json:"serverless,omitempty"`
	StorageMounts *[]runtimev1alpha1.GPUUnitStorageMount `json:"storageMounts,omitempty"`
}

// NormalizeCreateGPUUnitRequest trims, defaults, and validates one create request.
func NormalizeCreateGPUUnitRequest(req CreateGPUUnitRequest) (CreateGPUUnitRequest, error) {
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" {
		return CreateGPUUnitRequest{}, &ValidationError{Message: "operationID is required"}
	}

	_, name, err := NormalizeGPUUnitObjectKey("", req.Name)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.Name = name

	req.SpecName = strings.TrimSpace(req.SpecName)
	if req.SpecName == "" {
		return CreateGPUUnitRequest{}, &ValidationError{Message: "specName is required"}
	}

	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		return CreateGPUUnitRequest{}, &ValidationError{Message: "image is required"}
	}

	template, err := NormalizeGPUUnitTemplate(req.Template)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.Template = template

	access, err := NormalizeGPUUnitAccess(req.Access, req.Template.Ports)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.Access = access

	ssh, err := NormalizeGPUUnitSSH(req.Name, runtimev1alpha1.DefaultInstanceNamespace, req.SSH)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.SSH = ssh

	serverless, err := NormalizeGPUUnitServerless(req.Serverless)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.Serverless = serverless

	mounts, err := NormalizeGPUUnitStorageMounts(req.StorageMounts)
	if err != nil {
		return CreateGPUUnitRequest{}, err
	}
	req.StorageMounts = mounts

	return req, nil
}

// NormalizeUpdateGPUUnitRequest trims, defaults, and validates one update request.
func NormalizeUpdateGPUUnitRequest(unitName, namespace string, req UpdateGPUUnitRequest) (UpdateGPUUnitRequest, error) {
	req.Image = strings.TrimSpace(req.Image)

	template, err := NormalizeGPUUnitTemplate(req.Template)
	if err != nil {
		return UpdateGPUUnitRequest{}, err
	}
	req.Template = template

	req.Access.PrimaryPort = strings.TrimSpace(req.Access.PrimaryPort)
	req.Access.Scheme = strings.ToLower(strings.TrimSpace(req.Access.Scheme))

	if req.SSH != nil {
		normalized, err := NormalizeGPUUnitSSH(unitName, namespace, *req.SSH)
		if err != nil {
			return UpdateGPUUnitRequest{}, err
		}
		req.SSH = &normalized
	}

	if req.Serverless != nil {
		normalized, err := NormalizeGPUUnitServerless(*req.Serverless)
		if err != nil {
			return UpdateGPUUnitRequest{}, err
		}
		req.Serverless = &normalized
	}

	if req.StorageMounts != nil {
		mounts, err := NormalizeGPUUnitStorageMounts(*req.StorageMounts)
		if err != nil {
			return UpdateGPUUnitRequest{}, err
		}
		req.StorageMounts = &mounts
	}

	return req, nil
}

// NormalizeGPUUnitObjectKey defaults the instance namespace and validates namespace + name.
func NormalizeGPUUnitObjectKey(namespace, name string) (string, string, error) {
	ns, err := NormalizeGPUUnitInstanceNamespace(namespace)
	if err != nil {
		return "", "", err
	}

	trimmedName := strings.ToLower(strings.TrimSpace(name))
	if trimmedName == "" {
		return "", "", &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(trimmedName); len(errs) > 0 {
		return "", "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", trimmedName, strings.Join(errs, ", ")),
		}
	}

	return ns, trimmedName, nil
}

// NormalizeGPUUnitInstanceNamespace pins GPUUnit runtime objects to the shared instance namespace.
func NormalizeGPUUnitInstanceNamespace(namespace string) (string, error) {
	trimmed := strings.TrimSpace(namespace)
	if trimmed == "" || trimmed == runtimev1alpha1.DefaultInstanceNamespace {
		return runtimev1alpha1.DefaultInstanceNamespace, nil
	}
	return "", &ValidationError{
		Message: fmt.Sprintf("namespace is fixed to %q for active gpu units", runtimev1alpha1.DefaultInstanceNamespace),
	}
}

// IsZeroGPUUnitTemplate reports whether an update omitted template changes.
func IsZeroGPUUnitTemplate(t runtimev1alpha1.GPUUnitTemplate) bool {
	return len(t.Command) == 0 && len(t.Args) == 0 && len(t.Envs) == 0 && len(t.Ports) == 0
}

// IsZeroGPUUnitAccess reports whether an update omitted access changes.
func IsZeroGPUUnitAccess(access runtimev1alpha1.GPUUnitAccess) bool {
	return strings.TrimSpace(access.PrimaryPort) == "" && strings.TrimSpace(access.Scheme) == ""
}

// NormalizeGPUUnitTemplate validates the user-controlled pod slice used by active units.
func NormalizeGPUUnitTemplate(t runtimev1alpha1.GPUUnitTemplate) (runtimev1alpha1.GPUUnitTemplate, error) {
	seenEnvNames := map[string]struct{}{}
	for i := range t.Envs {
		t.Envs[i].Name = strings.TrimSpace(t.Envs[i].Name)
		if t.Envs[i].Name == "" {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{Message: "template env name is required"}
		}
		if errs := validation.IsEnvVarName(t.Envs[i].Name); len(errs) > 0 {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template env %q is invalid: %s", t.Envs[i].Name, strings.Join(errs, ", ")),
			}
		}
		if _, exists := seenEnvNames[t.Envs[i].Name]; exists {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template env %q is duplicated", t.Envs[i].Name),
			}
		}
		seenEnvNames[t.Envs[i].Name] = struct{}{}
	}

	seenPortNames := map[string]struct{}{}
	seenPortNumbers := map[int32]struct{}{}
	for i := range t.Ports {
		t.Ports[i].Name = strings.TrimSpace(t.Ports[i].Name)
		if t.Ports[i].Name == "" {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{Message: "template port name is required"}
		}
		if errs := validation.IsValidPortName(t.Ports[i].Name); len(errs) > 0 {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port name %q is invalid: %s", t.Ports[i].Name, strings.Join(errs, ", ")),
			}
		}
		if t.Ports[i].Port <= 0 || t.Ports[i].Port > 65535 {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port %d is out of range", t.Ports[i].Port),
			}
		}
		if t.Ports[i].Protocol == "" {
			t.Ports[i].Protocol = "TCP"
		}
		if _, exists := seenPortNames[t.Ports[i].Name]; exists {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port name %q is duplicated", t.Ports[i].Name),
			}
		}
		if _, exists := seenPortNumbers[t.Ports[i].Port]; exists {
			return runtimev1alpha1.GPUUnitTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port %d is duplicated", t.Ports[i].Port),
			}
		}
		seenPortNames[t.Ports[i].Name] = struct{}{}
		seenPortNumbers[t.Ports[i].Port] = struct{}{}
	}

	return t, nil
}

// NormalizeGPUUnitAccess validates the named access port against the runtime template.
func NormalizeGPUUnitAccess(access runtimev1alpha1.GPUUnitAccess, ports []runtimev1alpha1.GPUUnitPortSpec) (runtimev1alpha1.GPUUnitAccess, error) {
	access.PrimaryPort = strings.TrimSpace(access.PrimaryPort)
	access.Scheme = strings.ToLower(strings.TrimSpace(access.Scheme))
	if access.Scheme == "" {
		access.Scheme = runtimev1alpha1.DefaultAccessScheme
	}

	if len(ports) == 0 {
		if access.PrimaryPort != "" {
			return runtimev1alpha1.GPUUnitAccess{}, &ValidationError{
				Message: fmt.Sprintf("access.primaryPort %q requires at least one runtime port", access.PrimaryPort),
			}
		}
		return access, nil
	}

	if access.PrimaryPort == "" {
		access.PrimaryPort = ports[0].Name
	}

	for _, port := range ports {
		if port.Name == access.PrimaryPort {
			return access, nil
		}
	}

	return runtimev1alpha1.GPUUnitAccess{}, &ValidationError{
		Message: fmt.Sprintf("access.primaryPort %q does not exist in template.ports", access.PrimaryPort),
	}
}

// NormalizeGPUUnitSSH validates and defaults the optional user-facing SSH contract.
func NormalizeGPUUnitSSH(unitName, namespace string, ssh runtimev1alpha1.GPUUnitSSHSpec) (runtimev1alpha1.GPUUnitSSHSpec, error) {
	username := strings.ToLower(strings.TrimSpace(ssh.Username))
	serverAddr := strings.TrimSpace(ssh.ServerAddr)
	connectHost := strings.TrimSpace(ssh.ConnectHost)
	domainSuffix := strings.TrimPrefix(strings.TrimSpace(ssh.DomainSuffix), ".")
	clientName := strings.ToLower(strings.TrimSpace(ssh.ClientName))
	clientDomain := strings.ToLower(strings.TrimSpace(ssh.ClientDomain))
	token := strings.TrimSpace(ssh.Token)
	image := strings.TrimSpace(ssh.Image)
	frpImage := strings.TrimSpace(ssh.FRPImage)
	keys := make([]string, 0, len(ssh.AuthorizedKeys))
	seen := map[string]struct{}{}
	for _, key := range ssh.AuthorizedKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		keys = append(keys, trimmed)
	}

	if !ssh.Enabled {
		return runtimev1alpha1.GPUUnitSSHSpec{}, nil
	}
	if username == "" {
		username = runtimev1alpha1.DefaultUnitSSHUsername
	}
	if errs := validation.IsDNS1123Label(username); len(errs) > 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
			Message: fmt.Sprintf("ssh.username %q is invalid: %s", username, strings.Join(errs, ", ")),
		}
	}
	if len(keys) == 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
			Message: "ssh.authorizedKeys requires at least one public key when ssh.enabled is true",
		}
	}
	if serverAddr == "" {
		return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
			Message: "ssh.serverAddr is required when ssh.enabled is true",
		}
	}
	if ssh.Port == 0 {
		ssh.Port = runtimev1alpha1.DefaultUnitSSHPort
	}
	if ssh.ServerPort == 0 {
		ssh.ServerPort = runtimev1alpha1.DefaultUnitSSHFRPPort
	}
	if connectHost == "" {
		connectHost = serverAddr
	}
	if ssh.ConnectPort == 0 {
		ssh.ConnectPort = runtimev1alpha1.DefaultUnitSSHProxyPort
	}
	if image == "" {
		image = runtimev1alpha1.DefaultUnitSSHImage
	}
	if frpImage == "" {
		frpImage = runtimev1alpha1.DefaultUnitSSHFRPImage
	}
	if clientDomain == "" {
		if domainSuffix == "" {
			return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
				Message: "ssh.domainSuffix is required when ssh.clientDomain is not set",
			}
		}
		clientDomain = fmt.Sprintf("%s.%s.%s", unitName, namespace, domainSuffix)
	}
	if clientName != "" {
		if errs := validation.IsDNS1123Subdomain(clientName); len(errs) > 0 {
			return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
				Message: fmt.Sprintf("ssh.clientName %q is invalid: %s", clientName, strings.Join(errs, ", ")),
			}
		}
	}
	if domainSuffix != "" {
		if errs := validation.IsDNS1123Subdomain(domainSuffix); len(errs) > 0 {
			return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
				Message: fmt.Sprintf("ssh.domainSuffix %q is invalid: %s", domainSuffix, strings.Join(errs, ", ")),
			}
		}
	}
	if errs := validation.IsDNS1123Subdomain(clientDomain); len(errs) > 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
			Message: fmt.Sprintf("ssh.clientDomain %q is invalid: %s", clientDomain, strings.Join(errs, ", ")),
		}
	}
	for field, value := range map[string]int32{
		"ssh.port":        ssh.Port,
		"ssh.serverPort":  ssh.ServerPort,
		"ssh.connectPort": ssh.ConnectPort,
	} {
		if value < 1 || value > 65535 {
			return runtimev1alpha1.GPUUnitSSHSpec{}, &ValidationError{
				Message: fmt.Sprintf("%s must be between 1 and 65535", field),
			}
		}
	}

	return runtimev1alpha1.GPUUnitSSHSpec{
		Enabled:        true,
		Username:       username,
		AuthorizedKeys: keys,
		Port:           ssh.Port,
		ServerAddr:     serverAddr,
		ServerPort:     ssh.ServerPort,
		ConnectHost:    connectHost,
		ConnectPort:    ssh.ConnectPort,
		DomainSuffix:   domainSuffix,
		ClientName:     clientName,
		ClientDomain:   clientDomain,
		Token:          token,
		Image:          image,
		FRPImage:       frpImage,
	}, nil
}

// NormalizeGPUUnitStorageMounts validates the runtime storage attachment slice.
func NormalizeGPUUnitStorageMounts(mounts []runtimev1alpha1.GPUUnitStorageMount) ([]runtimev1alpha1.GPUUnitStorageMount, error) {
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
