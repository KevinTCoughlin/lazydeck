#pragma once

#include "CoreMinimal.h"
#include "Modules/ModuleInterface.h"

class FSpawnTabArgs;
class SDockTab;

/**
 * Registers the LazyDeck dock tab under the editor's Window menu. The
 * counterpart of the Godot addon's lazydeck_plugin.gd (@tool EditorPlugin)
 * and the Unity package's LazyDeckWindow's [MenuItem] registration.
 */
class FLazyDeckEditorModule : public IModuleInterface
{
public:
	virtual void StartupModule() override;
	virtual void ShutdownModule() override;

private:
	TSharedRef<SDockTab> SpawnTab(const FSpawnTabArgs& Args);

	static const FName TabId;
};
