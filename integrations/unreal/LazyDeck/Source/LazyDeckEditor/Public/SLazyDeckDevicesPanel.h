#pragma once

#include "CoreMinimal.h"
#include "LazyDeckClient.h"
#include "LazyDeckCookRunner.h"
#include "Widgets/SCompoundWidget.h"

class SCheckBox;
class SEditableTextBox;
class SMultiLineEditableTextBox;
class SVerticalBox;
template <typename OptionType> class SComboBox;
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
 * Also drives Unreal's own cook/package pipeline through UAT's BuildCookRun
 * (FLazyDeckCookRunner) so a build directory doesn't need to already exist
 * before Deploy can be used, mirroring Unity's in-process
 * BuildPipeline.BuildPlayer and the Godot addon's headless export
 * subprocess.
 *
 * Deliberately out of scope, matching both sibling integrations:
 * launch/stop (the SteamOS devkit protocol has no remote primitive for
 * either — see docs/DEVICE_LAUNCH.md).
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
	void RequestCapabilities(FLazyDeckConnectionInfo Info);
	void OnCapabilitiesResult(FLazyDeckApiResult Result, TSharedRef<FLazyDeckClient> NewClient, FLazyDeckConnectionInfo Info);
	void RefreshDevices();
	void OnDevicesResult(FLazyDeckApiResult Result);
	void Discover();
	void OnDiscoverResult(FLazyDeckApiResult Result);
	void PairSelected();
	void OnPairResult(FLazyDeckApiResult Result, FString DeviceId);
	void Deploy();
	void CookAndPackage();
	void OnCookComplete(FLazyDeckCookOutcome Outcome);
	void SyncLogs();
	void TrackJob(FLazyDeckApiResult SubmitResult, FString Label);
	void PollJob(const FString& Label, const FString& JobId);
	void OnPollResult(FLazyDeckApiResult Result, FString Label, FString JobId);
	void CancelCurrentJob();
	void OnCancelJobResult(FLazyDeckApiResult Result, FString JobId, bool bWasTrackedByPollLoop);
	void BrowseForDirectory(TSharedPtr<SEditableTextBox> TargetBox);

	FText GetStatusText() const;
	FReply OnConnectClicked();
	FReply OnDiscoverClicked();
	FReply OnPairClicked();
	FReply OnDeployClicked();
	FReply OnCookAndPackageClicked();
	FReply OnSyncLogsClicked();
	FReply OnCancelJobClicked();
	FReply OnBrowseDeployDirClicked();
	FReply OnBrowseLogsDirClicked();
	bool IsBusy() const
	{
		return bBusy || bCooking;
	}
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
	TSharedPtr<SEditableTextBox> LaunchArgsBox;
	TSharedPtr<SEditableTextBox> LogsDirBox;
	TSharedPtr<SMultiLineEditableTextBox> LogBox;
	FString LogText;

	TArray<TSharedPtr<FString>> CookPlatformOptions;
	TSharedPtr<FString> SelectedCookPlatform;
	TSharedPtr<SComboBox<TSharedPtr<FString>>> CookPlatformCombo;
	TSharedPtr<SCheckBox> CookDevelopmentCheckBox;
	bool bCooking = false;
};
