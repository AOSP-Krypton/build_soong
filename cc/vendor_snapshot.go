// Copyright 2020 The Android Open Source Project
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cc

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/blueprint/proptools"

	"android/soong/android"
)

// Defines the specifics of different images to which the snapshot process is
// applicable, e.g., vendor, recovery, ramdisk.
type image interface {
	// Used to register callbacks with the build system.
	init()

	// Function that returns true if the module is included in this image.
	// Using a function return instead of a value to prevent early
	// evalution of a function that may be not be defined.
	inImage(m *Module) func() bool

	// Returns the value of the "available" property for a given module for
	// and snapshot, e.g., "vendor_available", "recovery_available", etc.
	// or nil if the property is not defined.
	available(m *Module) *bool

	// Returns true if a dir under source tree is an SoC-owned proprietary
	// directory, such as device/, vendor/, etc.
	//
	// For a given snapshot (e.g., vendor, recovery, etc.) if
	// isProprietaryPath(dir) returns true, then the module in dir will be
	// built from sources.
	isProprietaryPath(dir string) bool

	// Whether to include VNDK in the snapshot for this image.
	includeVndk() bool

	// Whether a given module has been explicitly excluded from the
	// snapshot, e.g., using the exclude_from_vendor_snapshot or
	// exclude_from_recovery_snapshot properties.
	excludeFromSnapshot(m *Module) bool
}

type vendorImage struct{}
type recoveryImage struct{}

func (vendorImage) init() {
	android.RegisterSingletonType(
		"vendor-snapshot", VendorSnapshotSingleton)
	android.RegisterModuleType(
		"vendor_snapshot_shared", VendorSnapshotSharedFactory)
	android.RegisterModuleType(
		"vendor_snapshot_static", VendorSnapshotStaticFactory)
	android.RegisterModuleType(
		"vendor_snapshot_header", VendorSnapshotHeaderFactory)
	android.RegisterModuleType(
		"vendor_snapshot_binary", VendorSnapshotBinaryFactory)
	android.RegisterModuleType(
		"vendor_snapshot_object", VendorSnapshotObjectFactory)
}

func (vendorImage) inImage(m *Module) func() bool {
	return m.inVendor
}

func (vendorImage) available(m *Module) *bool {
	return m.VendorProperties.Vendor_available
}

func (vendorImage) isProprietaryPath(dir string) bool {
	return isVendorProprietaryPath(dir)
}

func (vendorImage) includeVndk() bool {
	return true
}

func (vendorImage) excludeFromSnapshot(m *Module) bool {
	return m.ExcludeFromVendorSnapshot()
}

func (recoveryImage) init() {
	android.RegisterSingletonType(
		"recovery-snapshot", RecoverySnapshotSingleton)
	android.RegisterModuleType(
		"recovery_snapshot_shared", RecoverySnapshotSharedFactory)
	android.RegisterModuleType(
		"recovery_snapshot_static", RecoverySnapshotStaticFactory)
	android.RegisterModuleType(
		"recovery_snapshot_header", RecoverySnapshotHeaderFactory)
	android.RegisterModuleType(
		"recovery_snapshot_binary", RecoverySnapshotBinaryFactory)
	android.RegisterModuleType(
		"recovery_snapshot_object", RecoverySnapshotObjectFactory)
}

func (recoveryImage) inImage(m *Module) func() bool {
	return m.InRecovery
}

func (recoveryImage) available(m *Module) *bool {
	return m.Properties.Recovery_available
}

func (recoveryImage) isProprietaryPath(dir string) bool {
	return isRecoveryProprietaryPath(dir)
}

func (recoveryImage) includeVndk() bool {
	return false
}

func (recoveryImage) excludeFromSnapshot(m *Module) bool {
	return m.ExcludeFromRecoverySnapshot()
}

var vendorImageSingleton vendorImage
var recoveryImageSingleton recoveryImage

const (
	vendorSnapshotHeaderSuffix = ".vendor_header."
	vendorSnapshotSharedSuffix = ".vendor_shared."
	vendorSnapshotStaticSuffix = ".vendor_static."
	vendorSnapshotBinarySuffix = ".vendor_binary."
	vendorSnapshotObjectSuffix = ".vendor_object."
)

const (
	recoverySnapshotHeaderSuffix = ".recovery_header."
	recoverySnapshotSharedSuffix = ".recovery_shared."
	recoverySnapshotStaticSuffix = ".recovery_static."
	recoverySnapshotBinarySuffix = ".recovery_binary."
	recoverySnapshotObjectSuffix = ".recovery_object."
)

var (
	vendorSnapshotsLock         sync.Mutex
	vendorSuffixModulesKey      = android.NewOnceKey("vendorSuffixModules")
	vendorSnapshotHeaderLibsKey = android.NewOnceKey("vendorSnapshotHeaderLibs")
	vendorSnapshotStaticLibsKey = android.NewOnceKey("vendorSnapshotStaticLibs")
	vendorSnapshotSharedLibsKey = android.NewOnceKey("vendorSnapshotSharedLibs")
	vendorSnapshotBinariesKey   = android.NewOnceKey("vendorSnapshotBinaries")
	vendorSnapshotObjectsKey    = android.NewOnceKey("vendorSnapshotObjects")
)

// vendor snapshot maps hold names of vendor snapshot modules per arch
func vendorSuffixModules(config android.Config) map[string]bool {
	return config.Once(vendorSuffixModulesKey, func() interface{} {
		return make(map[string]bool)
	}).(map[string]bool)
}

func vendorSnapshotHeaderLibs(config android.Config) *snapshotMap {
	return config.Once(vendorSnapshotHeaderLibsKey, func() interface{} {
		return newSnapshotMap()
	}).(*snapshotMap)
}

func vendorSnapshotSharedLibs(config android.Config) *snapshotMap {
	return config.Once(vendorSnapshotSharedLibsKey, func() interface{} {
		return newSnapshotMap()
	}).(*snapshotMap)
}

func vendorSnapshotStaticLibs(config android.Config) *snapshotMap {
	return config.Once(vendorSnapshotStaticLibsKey, func() interface{} {
		return newSnapshotMap()
	}).(*snapshotMap)
}

func vendorSnapshotBinaries(config android.Config) *snapshotMap {
	return config.Once(vendorSnapshotBinariesKey, func() interface{} {
		return newSnapshotMap()
	}).(*snapshotMap)
}

func vendorSnapshotObjects(config android.Config) *snapshotMap {
	return config.Once(vendorSnapshotObjectsKey, func() interface{} {
		return newSnapshotMap()
	}).(*snapshotMap)
}

type vendorSnapshotBaseProperties struct {
	// snapshot version.
	Version string

	// Target arch name of the snapshot (e.g. 'arm64' for variant 'aosp_arm64')
	Target_arch string
}

// vendorSnapshotModuleBase provides common basic functions for all snapshot modules.
type vendorSnapshotModuleBase struct {
	baseProperties vendorSnapshotBaseProperties
	moduleSuffix   string
}

func (p *vendorSnapshotModuleBase) Name(name string) string {
	return name + p.NameSuffix()
}

func (p *vendorSnapshotModuleBase) NameSuffix() string {
	versionSuffix := p.version()
	if p.arch() != "" {
		versionSuffix += "." + p.arch()
	}

	return p.moduleSuffix + versionSuffix
}

func (p *vendorSnapshotModuleBase) version() string {
	return p.baseProperties.Version
}

func (p *vendorSnapshotModuleBase) arch() string {
	return p.baseProperties.Target_arch
}

func (p *vendorSnapshotModuleBase) isSnapshotPrebuilt() bool {
	return true
}

// Call this after creating a snapshot module with module suffix
// such as vendorSnapshotSharedSuffix
func (p *vendorSnapshotModuleBase) init(m *Module, suffix string) {
	p.moduleSuffix = suffix
	m.AddProperties(&p.baseProperties)
	android.AddLoadHook(m, func(ctx android.LoadHookContext) {
		vendorSnapshotLoadHook(ctx, p)
	})
}

func vendorSnapshotLoadHook(ctx android.LoadHookContext, p *vendorSnapshotModuleBase) {
	if p.version() != ctx.DeviceConfig().VndkVersion() {
		ctx.Module().Disable()
		return
	}
}

type snapshotLibraryProperties struct {
	// Prebuilt file for each arch.
	Src *string `android:"arch_variant"`

	// list of directories that will be added to the include path (using -I).
	Export_include_dirs []string `android:"arch_variant"`

	// list of directories that will be added to the system path (using -isystem).
	Export_system_include_dirs []string `android:"arch_variant"`

	// list of flags that will be used for any module that links against this module.
	Export_flags []string `android:"arch_variant"`

	// Whether this prebuilt needs to depend on sanitize ubsan runtime or not.
	Sanitize_ubsan_dep *bool `android:"arch_variant"`

	// Whether this prebuilt needs to depend on sanitize minimal runtime or not.
	Sanitize_minimal_dep *bool `android:"arch_variant"`
}

type snapshotSanitizer interface {
	isSanitizerEnabled(t sanitizerType) bool
	setSanitizerVariation(t sanitizerType, enabled bool)
}

type snapshotLibraryDecorator struct {
	vendorSnapshotModuleBase
	*libraryDecorator
	properties          snapshotLibraryProperties
	sanitizerProperties struct {
		CfiEnabled bool `blueprint:"mutated"`

		// Library flags for cfi variant.
		Cfi snapshotLibraryProperties `android:"arch_variant"`
	}
	androidMkVendorSuffix bool
}

func (p *snapshotLibraryDecorator) linkerFlags(ctx ModuleContext, flags Flags) Flags {
	p.libraryDecorator.libName = strings.TrimSuffix(ctx.ModuleName(), p.NameSuffix())
	return p.libraryDecorator.linkerFlags(ctx, flags)
}

func (p *snapshotLibraryDecorator) matchesWithDevice(config android.DeviceConfig) bool {
	arches := config.Arches()
	if len(arches) == 0 || arches[0].ArchType.String() != p.arch() {
		return false
	}
	if !p.header() && p.properties.Src == nil {
		return false
	}
	return true
}

func (p *snapshotLibraryDecorator) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objs Objects) android.Path {
	m := ctx.Module().(*Module)
	p.androidMkVendorSuffix = vendorSuffixModules(ctx.Config())[m.BaseModuleName()]

	if p.header() {
		return p.libraryDecorator.link(ctx, flags, deps, objs)
	}

	if p.sanitizerProperties.CfiEnabled {
		p.properties = p.sanitizerProperties.Cfi
	}

	if !p.matchesWithDevice(ctx.DeviceConfig()) {
		return nil
	}

	p.libraryDecorator.reexportDirs(android.PathsForModuleSrc(ctx, p.properties.Export_include_dirs)...)
	p.libraryDecorator.reexportSystemDirs(android.PathsForModuleSrc(ctx, p.properties.Export_system_include_dirs)...)
	p.libraryDecorator.reexportFlags(p.properties.Export_flags...)

	in := android.PathForModuleSrc(ctx, *p.properties.Src)
	p.unstrippedOutputFile = in

	if p.shared() {
		libName := in.Base()
		builderFlags := flagsToBuilderFlags(flags)

		// Optimize out relinking against shared libraries whose interface hasn't changed by
		// depending on a table of contents file instead of the library itself.
		tocFile := android.PathForModuleOut(ctx, libName+".toc")
		p.tocFile = android.OptionalPathForPath(tocFile)
		transformSharedObjectToToc(ctx, in, tocFile, builderFlags)

		ctx.SetProvider(SharedLibraryInfoProvider, SharedLibraryInfo{
			SharedLibrary:           in,
			UnstrippedSharedLibrary: p.unstrippedOutputFile,

			TableOfContents: p.tocFile,
		})
	}

	if p.static() {
		depSet := android.NewDepSetBuilder(android.TOPOLOGICAL).Direct(in).Build()
		ctx.SetProvider(StaticLibraryInfoProvider, StaticLibraryInfo{
			StaticLibrary: in,

			TransitiveStaticLibrariesForOrdering: depSet,
		})
	}

	p.libraryDecorator.flagExporter.setProvider(ctx)

	return in
}

func (p *snapshotLibraryDecorator) install(ctx ModuleContext, file android.Path) {
	if p.matchesWithDevice(ctx.DeviceConfig()) && (p.shared() || p.static()) {
		p.baseInstaller.install(ctx, file)
	}
}

func (p *snapshotLibraryDecorator) nativeCoverage() bool {
	return false
}

func (p *snapshotLibraryDecorator) isSanitizerEnabled(t sanitizerType) bool {
	switch t {
	case cfi:
		return p.sanitizerProperties.Cfi.Src != nil
	default:
		return false
	}
}

func (p *snapshotLibraryDecorator) setSanitizerVariation(t sanitizerType, enabled bool) {
	if !enabled {
		return
	}
	switch t {
	case cfi:
		p.sanitizerProperties.CfiEnabled = true
	default:
		return
	}
}

func snapshotLibrary(suffix string) (*Module, *snapshotLibraryDecorator) {
	module, library := NewLibrary(android.DeviceSupported)

	module.stl = nil
	module.sanitize = nil
	library.disableStripping()

	prebuilt := &snapshotLibraryDecorator{
		libraryDecorator: library,
	}

	prebuilt.baseLinker.Properties.No_libcrt = BoolPtr(true)
	prebuilt.baseLinker.Properties.Nocrt = BoolPtr(true)

	// Prevent default system libs (libc, libm, and libdl) from being linked
	if prebuilt.baseLinker.Properties.System_shared_libs == nil {
		prebuilt.baseLinker.Properties.System_shared_libs = []string{}
	}

	module.compiler = nil
	module.linker = prebuilt
	module.installer = prebuilt

	prebuilt.init(module, suffix)
	module.AddProperties(
		&prebuilt.properties,
		&prebuilt.sanitizerProperties,
	)

	return module, prebuilt
}

func VendorSnapshotSharedFactory() android.Module {
	module, prebuilt := snapshotLibrary(vendorSnapshotSharedSuffix)
	prebuilt.libraryDecorator.BuildOnlyShared()
	return module.Init()
}

func RecoverySnapshotSharedFactory() android.Module {
	module, prebuilt := snapshotLibrary(recoverySnapshotSharedSuffix)
	prebuilt.libraryDecorator.BuildOnlyShared()
	return module.Init()
}

func VendorSnapshotStaticFactory() android.Module {
	module, prebuilt := snapshotLibrary(vendorSnapshotStaticSuffix)
	prebuilt.libraryDecorator.BuildOnlyStatic()
	return module.Init()
}

func RecoverySnapshotStaticFactory() android.Module {
	module, prebuilt := snapshotLibrary(recoverySnapshotStaticSuffix)
	prebuilt.libraryDecorator.BuildOnlyStatic()
	return module.Init()
}

func VendorSnapshotHeaderFactory() android.Module {
	module, prebuilt := snapshotLibrary(vendorSnapshotHeaderSuffix)
	prebuilt.libraryDecorator.HeaderOnly()
	return module.Init()
}

func RecoverySnapshotHeaderFactory() android.Module {
	module, prebuilt := snapshotLibrary(recoverySnapshotHeaderSuffix)
	prebuilt.libraryDecorator.HeaderOnly()
	return module.Init()
}

var _ snapshotSanitizer = (*snapshotLibraryDecorator)(nil)

type snapshotBinaryProperties struct {
	// Prebuilt file for each arch.
	Src *string `android:"arch_variant"`
}

type snapshotBinaryDecorator struct {
	vendorSnapshotModuleBase
	*binaryDecorator
	properties            snapshotBinaryProperties
	androidMkVendorSuffix bool
}

func (p *snapshotBinaryDecorator) matchesWithDevice(config android.DeviceConfig) bool {
	if config.DeviceArch() != p.arch() {
		return false
	}
	if p.properties.Src == nil {
		return false
	}
	return true
}

func (p *snapshotBinaryDecorator) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objs Objects) android.Path {
	if !p.matchesWithDevice(ctx.DeviceConfig()) {
		return nil
	}

	in := android.PathForModuleSrc(ctx, *p.properties.Src)
	stripFlags := flagsToStripFlags(flags)
	p.unstrippedOutputFile = in
	binName := in.Base()
	if p.stripper.NeedsStrip(ctx) {
		stripped := android.PathForModuleOut(ctx, "stripped", binName)
		p.stripper.StripExecutableOrSharedLib(ctx, in, stripped, stripFlags)
		in = stripped
	}

	m := ctx.Module().(*Module)
	p.androidMkVendorSuffix = vendorSuffixModules(ctx.Config())[m.BaseModuleName()]

	// use cpExecutable to make it executable
	outputFile := android.PathForModuleOut(ctx, binName)
	ctx.Build(pctx, android.BuildParams{
		Rule:        android.CpExecutable,
		Description: "prebuilt",
		Output:      outputFile,
		Input:       in,
	})

	return outputFile
}

func (p *snapshotBinaryDecorator) nativeCoverage() bool {
	return false
}

func VendorSnapshotBinaryFactory() android.Module {
	return snapshotBinaryFactory(vendorSnapshotBinarySuffix)
}

func RecoverySnapshotBinaryFactory() android.Module {
	return snapshotBinaryFactory(recoverySnapshotBinarySuffix)
}

func snapshotBinaryFactory(suffix string) android.Module {
	module, binary := NewBinary(android.DeviceSupported)
	binary.baseLinker.Properties.No_libcrt = BoolPtr(true)
	binary.baseLinker.Properties.Nocrt = BoolPtr(true)

	// Prevent default system libs (libc, libm, and libdl) from being linked
	if binary.baseLinker.Properties.System_shared_libs == nil {
		binary.baseLinker.Properties.System_shared_libs = []string{}
	}

	prebuilt := &snapshotBinaryDecorator{
		binaryDecorator: binary,
	}

	module.compiler = nil
	module.sanitize = nil
	module.stl = nil
	module.linker = prebuilt

	prebuilt.init(module, suffix)
	module.AddProperties(&prebuilt.properties)
	return module.Init()
}

type vendorSnapshotObjectProperties struct {
	// Prebuilt file for each arch.
	Src *string `android:"arch_variant"`
}

type snapshotObjectLinker struct {
	vendorSnapshotModuleBase
	objectLinker
	properties            vendorSnapshotObjectProperties
	androidMkVendorSuffix bool
}

func (p *snapshotObjectLinker) matchesWithDevice(config android.DeviceConfig) bool {
	if config.DeviceArch() != p.arch() {
		return false
	}
	if p.properties.Src == nil {
		return false
	}
	return true
}

func (p *snapshotObjectLinker) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objs Objects) android.Path {
	if !p.matchesWithDevice(ctx.DeviceConfig()) {
		return nil
	}

	m := ctx.Module().(*Module)
	p.androidMkVendorSuffix = vendorSuffixModules(ctx.Config())[m.BaseModuleName()]

	return android.PathForModuleSrc(ctx, *p.properties.Src)
}

func (p *snapshotObjectLinker) nativeCoverage() bool {
	return false
}

func VendorSnapshotObjectFactory() android.Module {
	module := newObject()

	prebuilt := &snapshotObjectLinker{
		objectLinker: objectLinker{
			baseLinker: NewBaseLinker(nil),
		},
	}
	module.linker = prebuilt

	prebuilt.init(module, vendorSnapshotObjectSuffix)
	module.AddProperties(&prebuilt.properties)
	return module.Init()
}

func RecoverySnapshotObjectFactory() android.Module {
	module := newObject()

	prebuilt := &snapshotObjectLinker{
		objectLinker: objectLinker{
			baseLinker: NewBaseLinker(nil),
		},
	}
	module.linker = prebuilt

	prebuilt.init(module, recoverySnapshotObjectSuffix)
	module.AddProperties(&prebuilt.properties)
	return module.Init()
}

func init() {
	vendorImageSingleton.init()
	recoveryImageSingleton.init()
}

var vendorSnapshotSingleton = snapshotSingleton{
	"vendor",
	"SOONG_VENDOR_SNAPSHOT_ZIP",
	android.OptionalPath{},
	true,
	vendorImageSingleton,
}

var recoverySnapshotSingleton = snapshotSingleton{
	"recovery",
	"SOONG_RECOVERY_SNAPSHOT_ZIP",
	android.OptionalPath{},
	false,
	recoveryImageSingleton,
}

func VendorSnapshotSingleton() android.Singleton {
	return &vendorSnapshotSingleton
}

func RecoverySnapshotSingleton() android.Singleton {
	return &recoverySnapshotSingleton
}

type snapshotSingleton struct {
	// Name, e.g., "vendor", "recovery", "ramdisk".
	name string

	// Make variable that points to the snapshot file, e.g.,
	// "SOONG_RECOVERY_SNAPSHOT_ZIP".
	makeVar string

	// Path to the snapshot zip file.
	snapshotZipFile android.OptionalPath

	// Whether the image supports VNDK extension modules.
	supportsVndkExt bool

	// Implementation of the image interface specific to the image
	// associated with this snapshot (e.g., specific to the vendor image,
	// recovery image, etc.).
	image image
}

var (
	// Modules under following directories are ignored. They are OEM's and vendor's
	// proprietary modules(device/, kernel/, vendor/, and hardware/).
	// TODO(b/65377115): Clean up these with more maintainable way
	vendorProprietaryDirs = []string{
		"device",
		"kernel",
		"vendor",
		"hardware",
	}

	// Modules under following directories are ignored. They are OEM's and vendor's
	// proprietary modules(device/, kernel/, vendor/, and hardware/).
	// TODO(b/65377115): Clean up these with more maintainable way
	recoveryProprietaryDirs = []string{
		"bootable/recovery",
		"device",
		"hardware",
		"kernel",
		"vendor",
	}

	// Modules under following directories are included as they are in AOSP,
	// although hardware/ and kernel/ are normally for vendor's own.
	// TODO(b/65377115): Clean up these with more maintainable way
	aospDirsUnderProprietary = []string{
		"kernel/configs",
		"kernel/prebuilts",
		"kernel/tests",
		"hardware/interfaces",
		"hardware/libhardware",
		"hardware/libhardware_legacy",
		"hardware/ril",
	}
)

// Determine if a dir under source tree is an SoC-owned proprietary directory, such as
// device/, vendor/, etc.
func isVendorProprietaryPath(dir string) bool {
	return isProprietaryPath(dir, vendorProprietaryDirs)
}

func isRecoveryProprietaryPath(dir string) bool {
	return isProprietaryPath(dir, recoveryProprietaryDirs)
}

// Determine if a dir under source tree is an SoC-owned proprietary directory, such as
// device/, vendor/, etc.
func isProprietaryPath(dir string, proprietaryDirs []string) bool {
	for _, p := range proprietaryDirs {
		if strings.HasPrefix(dir, p) {
			// filter out AOSP defined directories, e.g. hardware/interfaces/
			aosp := false
			for _, p := range aospDirsUnderProprietary {
				if strings.HasPrefix(dir, p) {
					aosp = true
					break
				}
			}
			if !aosp {
				return true
			}
		}
	}
	return false
}

func isVendorProprietaryModule(ctx android.BaseModuleContext) bool {

	// Any module in a vendor proprietary path is a vendor proprietary
	// module.

	if isVendorProprietaryPath(ctx.ModuleDir()) {
		return true
	}

	// However if the module is not in a vendor proprietary path, it may
	// still be a vendor proprietary module. This happens for cc modules
	// that are excluded from the vendor snapshot, and it means that the
	// vendor has assumed control of the framework-provided module.

	if c, ok := ctx.Module().(*Module); ok {
		if c.ExcludeFromVendorSnapshot() {
			return true
		}
	}

	return false
}

// Determine if a module is going to be included in vendor snapshot or not.
//
// Targets of vendor snapshot are "vendor: true" or "vendor_available: true" modules in
// AOSP. They are not guaranteed to be compatible with older vendor images. (e.g. might
// depend on newer VNDK) So they are captured as vendor snapshot To build older vendor
// image and newer system image altogether.
func isVendorSnapshotModule(m *Module, inVendorProprietaryPath bool, apexInfo android.ApexInfo) bool {
	return isSnapshotModule(m, inVendorProprietaryPath, apexInfo, vendorImageSingleton)
}

func isRecoverySnapshotModule(m *Module, inRecoveryProprietaryPath bool, apexInfo android.ApexInfo) bool {
	return isSnapshotModule(m, inRecoveryProprietaryPath, apexInfo, recoveryImageSingleton)
}

func isSnapshotModule(m *Module, inProprietaryPath bool, apexInfo android.ApexInfo, image image) bool {
	if !m.Enabled() || m.Properties.HideFromMake {
		return false
	}
	// When android/prebuilt.go selects between source and prebuilt, it sets
	// SkipInstall on the other one to avoid duplicate install rules in make.
	if m.IsSkipInstall() {
		return false
	}
	// skip proprietary modules, but (for the vendor snapshot only)
	// include all VNDK (static)
	if inProprietaryPath && (!image.includeVndk() || !m.IsVndk()) {
		return false
	}
	// If the module would be included based on its path, check to see if
	// the module is marked to be excluded. If so, skip it.
	if m.ExcludeFromVendorSnapshot() {
		return false
	}
	if m.Target().Os.Class != android.Device {
		return false
	}
	if m.Target().NativeBridge == android.NativeBridgeEnabled {
		return false
	}
	// the module must be installed in /vendor
	if !apexInfo.IsForPlatform() || m.isSnapshotPrebuilt() || !image.inImage(m)() {
		return false
	}
	// skip kernel_headers which always depend on vendor
	if _, ok := m.linker.(*kernelHeadersDecorator); ok {
		return false
	}
	// skip llndk_library and llndk_headers which are backward compatible
	if _, ok := m.linker.(*llndkStubDecorator); ok {
		return false
	}
	if _, ok := m.linker.(*llndkHeadersDecorator); ok {
		return false
	}

	// Libraries
	if l, ok := m.linker.(snapshotLibraryInterface); ok {
		// TODO(b/65377115): add full support for sanitizer
		if m.sanitize != nil {
			// scs and hwasan export both sanitized and unsanitized variants for static and header
			// Always use unsanitized variants of them.
			for _, t := range []sanitizerType{scs, hwasan} {
				if !l.shared() && m.sanitize.isSanitizerEnabled(t) {
					return false
				}
			}
			// cfi also exports both variants. But for static, we capture both.
			if !l.static() && !l.shared() && m.sanitize.isSanitizerEnabled(cfi) {
				return false
			}
		}
		if l.static() {
			return m.outputFile.Valid() && proptools.BoolDefault(image.available(m), true)
		}
		if l.shared() {
			if !m.outputFile.Valid() {
				return false
			}
			if image.includeVndk() {
				if !m.IsVndk() {
					return true
				}
				return m.isVndkExt()
			}
		}
		return true
	}

	// Binaries and Objects
	if m.binary() || m.object() {
		return m.outputFile.Valid() && proptools.BoolDefault(image.available(m), true)
	}

	return false
}

func (c *snapshotSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	// BOARD_VNDK_VERSION must be set to 'current' in order to generate a vendor snapshot.
	if ctx.DeviceConfig().VndkVersion() != "current" {
		return
	}

	var snapshotOutputs android.Paths

	/*
		Vendor snapshot zipped artifacts directory structure:
		{SNAPSHOT_ARCH}/
			arch-{TARGET_ARCH}-{TARGET_ARCH_VARIANT}/
				shared/
					(.so shared libraries)
				static/
					(.a static libraries)
				header/
					(header only libraries)
				binary/
					(executable binaries)
				object/
					(.o object files)
			arch-{TARGET_2ND_ARCH}-{TARGET_2ND_ARCH_VARIANT}/
				shared/
					(.so shared libraries)
				static/
					(.a static libraries)
				header/
					(header only libraries)
				binary/
					(executable binaries)
				object/
					(.o object files)
			NOTICE_FILES/
				(notice files, e.g. libbase.txt)
			configs/
				(config files, e.g. init.rc files, vintf_fragments.xml files, etc.)
			include/
				(header files of same directory structure with source tree)
	*/

	snapshotDir := c.name + "-snapshot"
	snapshotArchDir := filepath.Join(snapshotDir, ctx.DeviceConfig().DeviceArch())

	includeDir := filepath.Join(snapshotArchDir, "include")
	configsDir := filepath.Join(snapshotArchDir, "configs")
	noticeDir := filepath.Join(snapshotArchDir, "NOTICE_FILES")

	installedNotices := make(map[string]bool)
	installedConfigs := make(map[string]bool)

	var headers android.Paths

	installSnapshot := func(m *Module) android.Paths {
		targetArch := "arch-" + m.Target().Arch.ArchType.String()
		if m.Target().Arch.ArchVariant != "" {
			targetArch += "-" + m.Target().Arch.ArchVariant
		}

		var ret android.Paths

		prop := struct {
			ModuleName          string `json:",omitempty"`
			RelativeInstallPath string `json:",omitempty"`

			// library flags
			ExportedDirs       []string `json:",omitempty"`
			ExportedSystemDirs []string `json:",omitempty"`
			ExportedFlags      []string `json:",omitempty"`
			Sanitize           string   `json:",omitempty"`
			SanitizeMinimalDep bool     `json:",omitempty"`
			SanitizeUbsanDep   bool     `json:",omitempty"`

			// binary flags
			Symlinks []string `json:",omitempty"`

			// dependencies
			SharedLibs  []string `json:",omitempty"`
			RuntimeLibs []string `json:",omitempty"`
			Required    []string `json:",omitempty"`

			// extra config files
			InitRc         []string `json:",omitempty"`
			VintfFragments []string `json:",omitempty"`
		}{}

		// Common properties among snapshots.
		prop.ModuleName = ctx.ModuleName(m)
		if c.supportsVndkExt && m.isVndkExt() {
			// vndk exts are installed to /vendor/lib(64)?/vndk(-sp)?
			if m.isVndkSp() {
				prop.RelativeInstallPath = "vndk-sp"
			} else {
				prop.RelativeInstallPath = "vndk"
			}
		} else {
			prop.RelativeInstallPath = m.RelativeInstallPath()
		}
		prop.RuntimeLibs = m.Properties.SnapshotRuntimeLibs
		prop.Required = m.RequiredModuleNames()
		for _, path := range m.InitRc() {
			prop.InitRc = append(prop.InitRc, filepath.Join("configs", path.Base()))
		}
		for _, path := range m.VintfFragments() {
			prop.VintfFragments = append(prop.VintfFragments, filepath.Join("configs", path.Base()))
		}

		// install config files. ignores any duplicates.
		for _, path := range append(m.InitRc(), m.VintfFragments()...) {
			out := filepath.Join(configsDir, path.Base())
			if !installedConfigs[out] {
				installedConfigs[out] = true
				ret = append(ret, copyFile(ctx, path, out))
			}
		}

		var propOut string

		if l, ok := m.linker.(snapshotLibraryInterface); ok {
			exporterInfo := ctx.ModuleProvider(m, FlagExporterInfoProvider).(FlagExporterInfo)

			// library flags
			prop.ExportedFlags = exporterInfo.Flags
			for _, dir := range exporterInfo.IncludeDirs {
				prop.ExportedDirs = append(prop.ExportedDirs, filepath.Join("include", dir.String()))
			}
			for _, dir := range exporterInfo.SystemIncludeDirs {
				prop.ExportedSystemDirs = append(prop.ExportedSystemDirs, filepath.Join("include", dir.String()))
			}
			// shared libs dependencies aren't meaningful on static or header libs
			if l.shared() {
				prop.SharedLibs = m.Properties.SnapshotSharedLibs
			}
			if l.static() && m.sanitize != nil {
				prop.SanitizeMinimalDep = m.sanitize.Properties.MinimalRuntimeDep || enableMinimalRuntime(m.sanitize)
				prop.SanitizeUbsanDep = m.sanitize.Properties.UbsanRuntimeDep || enableUbsanRuntime(m.sanitize)
			}

			var libType string
			if l.static() {
				libType = "static"
			} else if l.shared() {
				libType = "shared"
			} else {
				libType = "header"
			}

			var stem string

			// install .a or .so
			if libType != "header" {
				libPath := m.outputFile.Path()
				stem = libPath.Base()
				if l.static() && m.sanitize != nil && m.sanitize.isSanitizerEnabled(cfi) {
					// both cfi and non-cfi variant for static libraries can exist.
					// attach .cfi to distinguish between cfi and non-cfi.
					// e.g. libbase.a -> libbase.cfi.a
					ext := filepath.Ext(stem)
					stem = strings.TrimSuffix(stem, ext) + ".cfi" + ext
					prop.Sanitize = "cfi"
					prop.ModuleName += ".cfi"
				}
				snapshotLibOut := filepath.Join(snapshotArchDir, targetArch, libType, stem)
				ret = append(ret, copyFile(ctx, libPath, snapshotLibOut))
			} else {
				stem = ctx.ModuleName(m)
			}

			propOut = filepath.Join(snapshotArchDir, targetArch, libType, stem+".json")
		} else if m.binary() {
			// binary flags
			prop.Symlinks = m.Symlinks()
			prop.SharedLibs = m.Properties.SnapshotSharedLibs

			// install bin
			binPath := m.outputFile.Path()
			snapshotBinOut := filepath.Join(snapshotArchDir, targetArch, "binary", binPath.Base())
			ret = append(ret, copyFile(ctx, binPath, snapshotBinOut))
			propOut = snapshotBinOut + ".json"
		} else if m.object() {
			// object files aren't installed to the device, so their names can conflict.
			// Use module name as stem.
			objPath := m.outputFile.Path()
			snapshotObjOut := filepath.Join(snapshotArchDir, targetArch, "object",
				ctx.ModuleName(m)+filepath.Ext(objPath.Base()))
			ret = append(ret, copyFile(ctx, objPath, snapshotObjOut))
			propOut = snapshotObjOut + ".json"
		} else {
			ctx.Errorf("unknown module %q in vendor snapshot", m.String())
			return nil
		}

		j, err := json.Marshal(prop)
		if err != nil {
			ctx.Errorf("json marshal to %q failed: %#v", propOut, err)
			return nil
		}
		ret = append(ret, writeStringToFile(ctx, string(j), propOut))

		return ret
	}

	ctx.VisitAllModules(func(module android.Module) {
		m, ok := module.(*Module)
		if !ok {
			return
		}

		moduleDir := ctx.ModuleDir(module)
		inProprietaryPath := c.image.isProprietaryPath(moduleDir)
		apexInfo := ctx.ModuleProvider(module, android.ApexInfoProvider).(android.ApexInfo)

		if m.ExcludeFromVendorSnapshot() {
			if inProprietaryPath {
				// Error: exclude_from_vendor_snapshot applies
				// to framework-path modules only.
				ctx.Errorf("module %q in vendor proprietary path %q may not use \"exclude_from_vendor_snapshot: true\"", m.String(), moduleDir)
				return
			}
			if Bool(c.image.available(m)) {
				// Error: may not combine "vendor_available:
				// true" with "exclude_from_vendor_snapshot:
				// true".
				ctx.Errorf(
					"module %q may not use both \""+
						c.name+
						"_available: true\" and \"exclude_from_vendor_snapshot: true\"",
					m.String())
				return
			}
		}

		if !isSnapshotModule(m, inProprietaryPath, apexInfo, c.image) {
			return
		}

		snapshotOutputs = append(snapshotOutputs, installSnapshot(m)...)
		if l, ok := m.linker.(snapshotLibraryInterface); ok {
			headers = append(headers, l.snapshotHeaders()...)
		}

		if len(m.NoticeFiles()) > 0 {
			noticeName := ctx.ModuleName(m) + ".txt"
			noticeOut := filepath.Join(noticeDir, noticeName)
			// skip already copied notice file
			if !installedNotices[noticeOut] {
				installedNotices[noticeOut] = true
				snapshotOutputs = append(snapshotOutputs, combineNotices(
					ctx, m.NoticeFiles(), noticeOut))
			}
		}
	})

	// install all headers after removing duplicates
	for _, header := range android.FirstUniquePaths(headers) {
		snapshotOutputs = append(snapshotOutputs, copyFile(
			ctx, header, filepath.Join(includeDir, header.String())))
	}

	// All artifacts are ready. Sort them to normalize ninja and then zip.
	sort.Slice(snapshotOutputs, func(i, j int) bool {
		return snapshotOutputs[i].String() < snapshotOutputs[j].String()
	})

	zipPath := android.PathForOutput(
		ctx,
		snapshotDir,
		c.name+"-"+ctx.Config().DeviceName()+".zip")
	zipRule := android.NewRuleBuilder(pctx, ctx)

	// filenames in rspfile from FlagWithRspFileInputList might be single-quoted. Remove it with tr
	snapshotOutputList := android.PathForOutput(
		ctx,
		snapshotDir,
		c.name+"-"+ctx.Config().DeviceName()+"_list")
	zipRule.Command().
		Text("tr").
		FlagWithArg("-d ", "\\'").
		FlagWithRspFileInputList("< ", snapshotOutputs).
		FlagWithOutput("> ", snapshotOutputList)

	zipRule.Temporary(snapshotOutputList)

	zipRule.Command().
		BuiltTool("soong_zip").
		FlagWithOutput("-o ", zipPath).
		FlagWithArg("-C ", android.PathForOutput(ctx, snapshotDir).String()).
		FlagWithInput("-l ", snapshotOutputList)

	zipRule.Build(zipPath.String(), c.name+" snapshot "+zipPath.String())
	zipRule.DeleteTemporaryFiles()
	c.snapshotZipFile = android.OptionalPathForPath(zipPath)
}

func (c *snapshotSingleton) MakeVars(ctx android.MakeVarsContext) {
	ctx.Strict(
		c.makeVar,
		c.snapshotZipFile.String())
}

type snapshotInterface interface {
	matchesWithDevice(config android.DeviceConfig) bool
}

var _ snapshotInterface = (*vndkPrebuiltLibraryDecorator)(nil)
var _ snapshotInterface = (*snapshotLibraryDecorator)(nil)
var _ snapshotInterface = (*snapshotBinaryDecorator)(nil)
var _ snapshotInterface = (*snapshotObjectLinker)(nil)

// gathers all snapshot modules for vendor, and disable unnecessary snapshots
// TODO(b/145966707): remove mutator and utilize android.Prebuilt to override source modules
func VendorSnapshotMutator(ctx android.BottomUpMutatorContext) {
	vndkVersion := ctx.DeviceConfig().VndkVersion()
	// don't need snapshot if current
	if vndkVersion == "current" || vndkVersion == "" {
		return
	}

	module, ok := ctx.Module().(*Module)
	if !ok || !module.Enabled() || module.VndkVersion() != vndkVersion {
		return
	}

	if !module.isSnapshotPrebuilt() {
		return
	}

	// isSnapshotPrebuilt ensures snapshotInterface
	if !module.linker.(snapshotInterface).matchesWithDevice(ctx.DeviceConfig()) {
		// Disable unnecessary snapshot module, but do not disable
		// vndk_prebuilt_shared because they might be packed into vndk APEX
		if !module.IsVndk() {
			module.Disable()
		}
		return
	}

	var snapshotMap *snapshotMap

	if lib, ok := module.linker.(libraryInterface); ok {
		if lib.static() {
			snapshotMap = vendorSnapshotStaticLibs(ctx.Config())
		} else if lib.shared() {
			snapshotMap = vendorSnapshotSharedLibs(ctx.Config())
		} else {
			// header
			snapshotMap = vendorSnapshotHeaderLibs(ctx.Config())
		}
	} else if _, ok := module.linker.(*snapshotBinaryDecorator); ok {
		snapshotMap = vendorSnapshotBinaries(ctx.Config())
	} else if _, ok := module.linker.(*snapshotObjectLinker); ok {
		snapshotMap = vendorSnapshotObjects(ctx.Config())
	} else {
		return
	}

	vendorSnapshotsLock.Lock()
	defer vendorSnapshotsLock.Unlock()
	snapshotMap.add(module.BaseModuleName(), ctx.Arch().ArchType, ctx.ModuleName())
}

// Disables source modules which have snapshots
func VendorSnapshotSourceMutator(ctx android.BottomUpMutatorContext) {
	if !ctx.Device() {
		return
	}

	vndkVersion := ctx.DeviceConfig().VndkVersion()
	// don't need snapshot if current
	if vndkVersion == "current" || vndkVersion == "" {
		return
	}

	module, ok := ctx.Module().(*Module)
	if !ok {
		return
	}

	// vendor suffix should be added to snapshots if the source module isn't vendor: true.
	if !module.SocSpecific() {
		// But we can't just check SocSpecific() since we already passed the image mutator.
		// Check ramdisk and recovery to see if we are real "vendor: true" module.
		ramdisk_available := module.InRamdisk() && !module.OnlyInRamdisk()
		vendor_ramdisk_available := module.InVendorRamdisk() && !module.OnlyInVendorRamdisk()
		recovery_available := module.InRecovery() && !module.OnlyInRecovery()

		if !ramdisk_available && !recovery_available && !vendor_ramdisk_available {
			vendorSnapshotsLock.Lock()
			defer vendorSnapshotsLock.Unlock()

			vendorSuffixModules(ctx.Config())[ctx.ModuleName()] = true
		}
	}

	if module.isSnapshotPrebuilt() || module.VndkVersion() != ctx.DeviceConfig().VndkVersion() {
		// only non-snapshot modules with BOARD_VNDK_VERSION
		return
	}

	// .. and also filter out llndk library
	if module.isLlndk(ctx.Config()) {
		return
	}

	var snapshotMap *snapshotMap

	if lib, ok := module.linker.(libraryInterface); ok {
		if lib.static() {
			snapshotMap = vendorSnapshotStaticLibs(ctx.Config())
		} else if lib.shared() {
			snapshotMap = vendorSnapshotSharedLibs(ctx.Config())
		} else {
			// header
			snapshotMap = vendorSnapshotHeaderLibs(ctx.Config())
		}
	} else if module.binary() {
		snapshotMap = vendorSnapshotBinaries(ctx.Config())
	} else if module.object() {
		snapshotMap = vendorSnapshotObjects(ctx.Config())
	} else {
		return
	}

	if _, ok := snapshotMap.get(ctx.ModuleName(), ctx.Arch().ArchType); !ok {
		// Corresponding snapshot doesn't exist
		return
	}

	// Disables source modules if corresponding snapshot exists.
	if lib, ok := module.linker.(libraryInterface); ok && lib.buildStatic() && lib.buildShared() {
		// But do not disable because the shared variant depends on the static variant.
		module.SkipInstall()
		module.Properties.HideFromMake = true
	} else {
		module.Disable()
	}
}
