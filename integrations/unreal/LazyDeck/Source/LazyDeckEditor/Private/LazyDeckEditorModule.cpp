#include "LazyDeckEditorModule.h"

#include "SLazyDeckDevicesPanel.h"
#include "WorkspaceMenuStructure.h"
#include "WorkspaceMenuStructureModule.h"
#include "Widgets/Docking/SDockTab.h"

#define LOCTEXT_NAMESPACE "FLazyDeckEditorModule"

const FName FLazyDeckEditorModule::TabId(TEXT("LazyDeck"));

void FLazyDeckEditorModule::StartupModule()
{
	FGlobalTabmanager::Get()
		->RegisterNomadTabSpawner(TabId, FOnSpawnTab::CreateRaw(this, &FLazyDeckEditorModule::SpawnTab))
		.SetDisplayName(LOCTEXT("TabTitle", "LazyDeck"))
		.SetTooltipText(LOCTEXT("TabTooltip", "Discover, pair, and deploy to Steam Deck / Steam Machine devkits"))
		.SetGroup(WorkspaceMenu::GetMenuStructure().GetToolsCategory());
}

void FLazyDeckEditorModule::ShutdownModule()
{
	if (FGlobalTabmanager::IsInitialized())
	{
		FGlobalTabmanager::Get()->UnregisterNomadTabSpawner(TabId);
	}
}

TSharedRef<SDockTab> FLazyDeckEditorModule::SpawnTab(const FSpawnTabArgs& Args)
{
	return SNew(SDockTab)
		.TabRole(ETabRole::NomadTab)
		[
			SNew(SLazyDeckDevicesPanel)
		];
}

#undef LOCTEXT_NAMESPACE

IMPLEMENT_MODULE(FLazyDeckEditorModule, LazyDeckEditor)
