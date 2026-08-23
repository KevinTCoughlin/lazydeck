using UnrealBuildTool;

public class LazyDeckEditor : ModuleRules
{
	public LazyDeckEditor(ReadOnlyTargetRules Target) : base(Target)
	{
		PCHUsage = PCHUsageMode.UseExplicitOrSharedPCHs;

		// Modern-module defaults recommended for new UE5 plugins: pin to the
		// current engine build-settings revision rather than silently
		// inheriting whatever a future engine version changes the default
		// to, require exact per-file includes (IWYU) instead of relying on
		// what a shared PCH happens to pull in transitively, and follow the
		// engine's own current default C++ standard rather than an older one
		// left over from whatever CppStandardVersion used to default to.
		DefaultBuildSettings = BuildSettingsVersion.Latest;
		IWYUSupport = IWYUSupport.Full;
		CppStandard = CppStandardVersion.Default;

		PublicDependencyModuleNames.AddRange(new string[]
		{
			"Core",
			"CoreUObject",
			"Engine",
		});

		// HTTP/Json talk to lazydeck serve's /v1 API; Slate/UnrealEd/ToolMenus/
		// WorkspaceMenuStructure build the dock tab, matching how the Godot
		// addon uses EditorPlugin.add_control_to_dock and the Unity package
		// uses EditorWindow.
		PrivateDependencyModuleNames.AddRange(new string[]
		{
			"Slate",
			"SlateCore",
			"UnrealEd",
			"HTTP",
			"Json",
			"JsonUtilities",
			"ToolMenus",
			"WorkspaceMenuStructure",
			"DesktopPlatform",
		});
	}
}
