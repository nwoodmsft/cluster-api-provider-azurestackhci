/*
Copyright 2020 The Kubernetes Authors.
Portions Copyright © Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package virtualmachines

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"

	"github.com/Azure/go-autorest/autorest/to"
	infrav1 "github.com/microsoft/cluster-api-provider-azurestackhci/api/v1beta1"
	azurestackhci "github.com/microsoft/cluster-api-provider-azurestackhci/cloud"
	"github.com/microsoft/cluster-api-provider-azurestackhci/cloud/converters"
	"github.com/microsoft/cluster-api-provider-azurestackhci/cloud/services/networkinterfaces"
	"github.com/microsoft/cluster-api-provider-azurestackhci/cloud/telemetry"
	infrav1util "github.com/microsoft/cluster-api-provider-azurestackhci/pkg/util"
	"github.com/microsoft/moc-sdk-for-go/services/compute"
	"github.com/microsoft/moc-sdk-for-go/services/network"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

const (
	computerNamePrefix = "moc-"
	computerNameLength = 15
)

// Spec input specification for Get/CreateOrUpdate/Delete calls
type Spec struct {
	Name                string
	NICName             string
	SSHKeyData          []string
	Size                string
	GpuCount            int32
	Zone                string
	Image               infrav1.Image
	OSDisk              infrav1.OSDisk
	CustomData          string
	VMType              compute.VMType
	StorageContainer    string
	AvailabilitySetName string
	PlacementGroupName  string
}

// Get provides information about a virtual machine.
func (s *Service) Get(ctx context.Context, spec interface{}) (interface{}, error) {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return compute.VirtualMachine{}, errors.New("invalid vm specification")
	}

	vm, err := s.Client.Get(ctx, s.Scope.GetResourceGroup(), vmSpec.Name)
	if err != nil {
		return nil, err
	}
	if vm == nil || len(*vm) == 0 {
		return nil, errors.Wrapf(err, "vm %s not found", vmSpec.Name)
	}

	return converters.SDKToVM((*vm)[0])
}

// Reconcile gets/creates/updates a virtual machine.
func (s *Service) Reconcile(ctx context.Context, spec interface{}) error {
	telemetry.WriteMocInfoLog(ctx, s.Scope)
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid vm specification")
	}

	storageProfile, err := generateStorageProfile(*vmSpec)
	if err != nil {
		return err
	}

	logger := s.Scope.GetLogger()
	logger.Info("getting nic", "nic", vmSpec.NICName)
	nicInterface, err := networkinterfaces.NewService(s.Scope).Get(ctx, &networkinterfaces.Spec{Name: vmSpec.NICName})
	if err != nil {
		return err
	}
	nic, ok := nicInterface.(network.Interface)
	if !ok {
		return errors.New("error getting network interface")
	}
	logger.Info("got nic", "nic", vmSpec.NICName)

	logger.Info("creating vm",
		"Name", vmSpec.Name,
		"NICName", vmSpec.NICName,
		"Size", vmSpec.Size,
		"GpuCount", vmSpec.GpuCount,
		"Image", vmSpec.Image,
		"OSDisk", vmSpec.OSDisk,
		"VMType", vmSpec.VMType,
		"AvailabilitySetName", vmSpec.AvailabilitySetName,
		"PlacementGroupName", vmSpec.PlacementGroupName,
	)

	sshKeyData := vmSpec.SSHKeyData
	if len(sshKeyData) == 0 {
		privateKey, perr := rsa.GenerateKey(rand.Reader, 2048)
		if perr != nil {
			return errors.Wrap(perr, "Failed to generate private key")
		}

		publicRsaKey, perr := ssh.NewPublicKey(&privateKey.PublicKey)
		if perr != nil {
			return errors.Wrap(perr, "Failed to generate public key")
		}
		sshKeyData = []string{string(ssh.MarshalAuthorizedKey(publicRsaKey))}
	}

	sshPublicKeys := []compute.SSHPublicKey{}
	sshKeyPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", azurestackhci.DefaultUserName)
	for i := 0; i < len(sshKeyData); i++ {
		sshPublicKeys = append(sshPublicKeys, compute.SSHPublicKey{
			Path:    &sshKeyPath,
			KeyData: &sshKeyData[i],
		})
	}

	computerName, err := generateComputerName(vmSpec.Image.OSType)
	if err != nil {
		return errors.Wrap(err, "Failed to generate computer name")
	}

	virtualMachine := compute.VirtualMachine{
		Name: to.StringPtr(vmSpec.Name),
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			StorageProfile: storageProfile,
			OsProfile: &compute.OSProfile{
				ComputerName:  to.StringPtr(computerName),
				AdminUsername: to.StringPtr(azurestackhci.DefaultUserName),
				AdminPassword: nil,
				CustomData:    to.StringPtr(vmSpec.CustomData),
				OsType:        compute.OperatingSystemTypes(vmSpec.OSDisk.OSType),
				LinuxConfiguration: &compute.LinuxConfiguration{
					SSH: &compute.SSHConfiguration{
						PublicKeys: &sshPublicKeys,
					},
					DisablePasswordAuthentication: to.BoolPtr(true),
				},
			},
			NetworkProfile: &compute.NetworkProfile{
				NetworkInterfaces: &[]compute.NetworkInterfaceReference{
					{
						ID: nic.Name,
					},
				},
			},
			VmType: vmSpec.VMType,
			HardwareProfile: &compute.HardwareProfile{
				VMSize: compute.VirtualMachineSizeTypes(vmSpec.Size),
			},
		},
	}

	virtualMachine.HardwareProfile.VirtualMachineGPUs = generateGpuList(vmSpec.GpuCount)

	if vmSpec.Image.OSType == infrav1.OSTypeWindows || vmSpec.Image.OSType == infrav1.OSTypeWindows2022 {
		virtualMachine.OsProfile.LinuxConfiguration = nil
		pass := ""
		virtualMachine.OsProfile.AdminPassword = &pass
		username := "Administrator"
		virtualMachine.OsProfile.AdminUsername = &username

		virtualMachine.OsProfile.WindowsConfiguration = &compute.WindowsConfiguration{
			SSH: &compute.SSHConfiguration{
				PublicKeys: &sshPublicKeys,
			},
		}
	}
	if vmSpec.AvailabilitySetName != "" {
		virtualMachine.VirtualMachineProperties.AvailabilitySetProfile = &compute.AvailabilitySetReference{
			Name:      to.StringPtr(vmSpec.AvailabilitySetName),
			GroupName: to.StringPtr(s.Scope.GetResourceGroup()),
		}
	}

	if vmSpec.PlacementGroupName != "" {
		virtualMachine.VirtualMachineProperties.PlacementGroupProfile = &compute.PlacementGroupReference{
			Name:      to.StringPtr(vmSpec.PlacementGroupName),
			GroupName: to.StringPtr(s.Scope.GetResourceGroup()),
		}
	}

	_, err = s.Client.CreateOrUpdate(
		ctx,
		s.Scope.GetResourceGroup(),
		vmSpec.Name,
		&virtualMachine)
	telemetry.WriteMocOperationLog(logger, telemetry.CreateOrUpdate, s.Scope.GetCustomResourceTypeWithName(), telemetry.VirtualMachine,
		telemetry.GenerateMocResourceName(s.Scope.GetResourceGroup(), vmSpec.Name), nil, err)
	if err != nil {
		return errors.Wrapf(err, "cannot create vm")
	}

	logger.Info("successfully created vm", "name", vmSpec.Name)
	return err
}

// Delete deletes the virtual machine with the provided name.
func (s *Service) Delete(ctx context.Context, spec interface{}) error {
	telemetry.WriteMocInfoLog(ctx, s.Scope)
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid vm Specification")
	}
	logger := s.Scope.GetLogger()
	logger.Info("deleting vm", "vm", vmSpec.Name)
	err := s.Client.Delete(ctx, s.Scope.GetResourceGroup(), vmSpec.Name)
	telemetry.WriteMocOperationLog(s.Scope.GetLogger(), telemetry.Delete, s.Scope.GetCustomResourceTypeWithName(), telemetry.VirtualMachine,
		telemetry.GenerateMocResourceName(s.Scope.GetResourceGroup(), vmSpec.Name), nil, err)
	if err != nil && azurestackhci.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "failed to delete vm %s in resource group %s", vmSpec.Name, s.Scope.GetResourceGroup())
	}

	logger.Info("successfully deleted vm", "vm", vmSpec.Name)
	return err
}

// generateStorageProfile generates a pointer to a compute.StorageProfile which can utilized for VM creation.
func generateStorageProfile(vmSpec Spec) (*compute.StorageProfile, error) {
	osDisk := &compute.OSDisk{
		Vhd: &compute.VirtualHardDisk{
			URI: to.StringPtr(azurestackhci.GenerateOSDiskName(vmSpec.Name)),
		},
	}
	dataDisks := make([]compute.DataDisk, 0)

	imageRef, err := generateImageReference(vmSpec.Image)
	if err != nil {
		return nil, errors.Wrapf(err, "error getting image reference")
	}

	storageProfile := &compute.StorageProfile{
		OsDisk:                osDisk,
		DataDisks:             &dataDisks,
		ImageReference:        imageRef,
		VmConfigContainerName: &vmSpec.StorageContainer,
	}

	return storageProfile, nil
}

// generateImageReference generates a pointer to a compute.ImageReference which can utilized for VM creation.
func generateImageReference(image infrav1.Image) (*compute.ImageReference, error) {
	imageRef := &compute.ImageReference{}

	if image.Name == nil {
		return nil, errors.New("Missing ImageReference")
	}
	imageRef.Name = to.StringPtr(*image.Name)

	if image.ID != nil {
		imageRef.ID = to.StringPtr(*image.ID)

		// return early since we should only need the image ID
		return imageRef, nil
	} else if image.SubscriptionID != nil && image.ResourceGroup != nil && image.Gallery != nil && image.Name != nil && image.Version != nil {
		imageID, err := generateImageID(image)
		if err != nil {
			return nil, err
		}

		imageRef.ID = to.StringPtr(imageID)

		// return early since we're referencing an image that may not be published
		return imageRef, nil
	}

	if image.Publisher != nil {
		imageRef.Publisher = image.Publisher
	}
	if image.Offer != nil {
		imageRef.Offer = image.Offer
	}
	if image.SKU != nil {
		imageRef.Sku = image.SKU
	}
	if image.Version != nil {
		imageRef.Version = image.Version

		return imageRef, nil
	}

	return nil, errors.Errorf("Image reference cannot be generated, as fields are missing: %+v", *imageRef)
}

// generateImageID generates the resource ID for an image stored in an AzureStackHCI Shared Image Gallery.
func generateImageID(image infrav1.Image) (string, error) {
	if image.SubscriptionID == nil {
		return "", errors.New("Image subscription ID cannot be nil when specifying an image from an AzureStackHCI Shared Image Gallery")
	}
	if image.ResourceGroup == nil {
		return "", errors.New("Image resource group cannot be nil when specifying an image from an AzureStackHCI Shared Image Gallery")
	}
	if image.Gallery == nil {
		return "", errors.New("Image gallery cannot be nil when specifying an image from an AzureStackHCI Shared Image Gallery")
	}
	if image.Name == nil {
		return "", errors.New("Image name cannot be nil when specifying an image from an AzureStackHCI Shared Image Gallery")
	}
	if image.Version == nil {
		return "", errors.New("Image version cannot be nil when specifying an image from an AzureStackHCI Shared Image Gallery")
	}

	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s", *image.SubscriptionID, *image.ResourceGroup, *image.Gallery, *image.Name, *image.Version), nil
}

// GenerateRandomString returns a URL-safe, base64 encoded
// securely generated random string.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func GenerateRandomString(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), err
}

// generateComputerName returns a unique OS computer name which is expected to be valid on any
// operating system. To satisfy Windows requirements, we generate a length-restricted name. The
// generated computer name has the following format: <prefix><os_identifier><random chars>
func generateComputerName(os infrav1.OSType) (string, error) {
	computerName := computerNamePrefix

	switch os {
	case infrav1.OSTypeWindows:
		computerName += "w"
	case infrav1.OSTypeWindows2022:
		computerName += "w"
	case infrav1.OSTypeLinux:
		computerName += "l"
	default: // Unknown OS
		computerName += "u"
	}

	if len(computerName) < computerNameLength {
		randomString, err := infrav1util.RandomAlphaNumericString(computerNameLength - len(computerName))
		if err != nil {
			return "", err
		}
		computerName += randomString
	}

	return computerName, nil
}

func generateGpuList(gpuCount int32) []*compute.VirtualMachineGPU {
	if gpuCount <= 0 {
		return nil
	}

	gpuList := make([]*compute.VirtualMachineGPU, gpuCount)
	gpuAssignment := compute.GpuDefault
	for i := 0; i < int(gpuCount); i++ {
		gpuList[i] = &compute.VirtualMachineGPU{
			Assignment: &gpuAssignment,
		}
	}
	return gpuList
}
