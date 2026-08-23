using UnrealBuildTool;

public class LazyDeckEditor : ModuleRules
{
	public LazyDeckEditor(ReadOnlyTargetRules Target) : base(Target)
	{
		PCHUsage = PCHUsageMode.UseExplicitOrSharedPCHs;

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
