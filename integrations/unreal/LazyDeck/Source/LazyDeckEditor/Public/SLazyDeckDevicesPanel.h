#pragma once

#include "CoreMinimal.h"
#include "LazyDeckClient.h"
#include "Widgets/SCompoundWidget.h"

class SEditableTextBox;
class SMultiLineEditableTextBox;
class SVerticalBox;
template <typename T> class SListView;

/** One row in the configured-devices list (api/openapi.yaml's Device schema). */
struct FLazyDeckDeviceRow
{
	FString Id;
	FString Machine;
	FString Login;
};

/**
 * The LazyDeck dock: connects to a running `lazydeck serve`, lists configured
 * devices, discovers devkits on the LAN, pairs a configured device, submits a
 * deploy/log-sync job against a user-supplied directory, and polls job
 * progress until it finishes. The Unreal counterpart of the Godot addon's
 * ui/devices_dock.gd and the Unity package's Editor/LazyDeckWindow.cs.
 *
 * Deliberately out of scope, matching both sibling integrations:
 * launch/stop (the SteamOS devkit protocol has no remote primitive for
 * either — see docs/DEVICE_LAUNCH.md) and driving Unreal's own
 * cook/package pipeline automatically (unlike Unity's in-process
 * BuildPipeline.BuildPlayer or Godot's headless export subprocess, hooking
 * Unreal's UAT-based packaging in is a separate follow-up; this panel
 * deploys whatever staged/cooked output directory the user already has).
 */
class SLazyDeckDevicesPanel : public SCompoundWidget
{
public:
	SLATE_BEGIN_ARGS(SLazyDeckDevicesPanel) {}
	SLATE_END_ARGS()

	void Construct(const FArguments& InArgs);

private:
	void Connect();
	void OnConnectAttempt(bool bAutoStarted, int32 RemainingAttempts);
	void OnCapabilitiesResult(FLazyDeckApiResult Result, FLazyDeckConnectionInfo Info);
	void RefreshDevices();
	void OnDevicesResult(FLazyDeckApiResult Result);
	void Discover();
	void OnDiscoverResult(FLazyDeckApiResult Result);
	void PairSelected();
	void OnPairResult(FLazyDeckApiResult Result, FString DeviceId);
	void Deploy();
	void SyncLogs();
	void TrackJob(FLazyDeckApiResult SubmitResult, FString Label);
	void PollJob(const FString& Label, const FString& JobId);
	void OnPollResult(FLazyDeckApiResult Result, FString Label, FString JobId);
	void CancelCurrentJob();
	void BrowseForDirectory(TSharedPtr<SEditableTextBox> TargetBox);

	FText GetStatusText() const;
	FReply OnConnectClicked();
	FReply OnDiscoverClicked();
	FReply OnPairClicked();
	FReply OnDeployClicked();
	FReply OnSyncLogsClicked();
	FReply OnCancelJobClicked();
	FReply OnBrowseDeployDirClicked();
	FReply OnBrowseLogsDirClicked();
	bool IsBusy() const { return bBusy; }
	void AppendLog(const FString& Line);

	TSharedPtr<FLazyDeckClient> Client;
	FLazyDeckConnectionInfo Connection;
	bool bConnected = false;
	bool bBusy = false;
	FString StatusText = TEXT("Not connected");
	FString CurrentJobId;

	TArray<TSharedPtr<FLazyDeckDeviceRow>> Devices;
	TSharedPtr<SListView<TSharedPtr<FLazyDeckDeviceRow>>> DeviceListView;
	TSharedPtr<FLazyDeckDeviceRow> SelectedDevice;

	TSharedPtr<SEditableTextBox> GameIdBox;
	TSharedPtr<SEditableTextBox> DeployDirBox;
	TSharedPtr<SEditableTextBox> LogsDirBox;
	TSharedPtr<SMultiLineEditableTextBox> LogBox;
	FString LogText;
};
