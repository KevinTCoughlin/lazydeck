#include "SLazyDeckDevicesPanel.h"

#include "DesktopPlatformModule.h"
#include "Dom/JsonObject.h"
#include "Editor.h"
#include "Framework/Application/SlateApplication.h"
#include "IDesktopPlatform.h"
#include "LazyDeckConnectionLocator.h"
#include "LazyDeckServerLauncher.h"
#include "Misc/Paths.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"
#include "TimerManager.h"
#include "Widgets/Input/SButton.h"
#include "Widgets/Input/SEditableTextBox.h"
#include "Widgets/Input/SMultiLineEditableTextBox.h"
#include "Widgets/Layout/SScrollBox.h"
#include "Widgets/SBoxPanel.h"
#include "Widgets/Text/STextBlock.h"
#include "Widgets/Views/SListView.h"
#include "Widgets/Views/STableRow.h"

#define LOCTEXT_NAMESPACE "LazyDeck"

namespace
{
FString ParseJobId(const FString& Body)
{
	TSharedPtr<FJsonObject> JsonObject;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Body);
	if (!FJsonSerializer::Deserialize(Reader, JsonObject) || !JsonObject.IsValid())
	{
		return FString();
	}
	const TSharedPtr<FJsonObject>* JobObject;
	if (!JsonObject->TryGetObjectField(TEXT("job"), JobObject))
	{
		return FString();
	}
	FString Id;
	(*JobObject)->TryGetStringField(TEXT("id"), Id);
	return Id;
}

bool ParseJobStatus(const FString& Body, FString& OutStatus, FString& OutMessage)
{
	TSharedPtr<FJsonObject> JsonObject;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Body);
	if (!FJsonSerializer::Deserialize(Reader, JsonObject) || !JsonObject.IsValid())
	{
		return false;
	}
	const TSharedPtr<FJsonObject>* JobObject;
	if (!JsonObject->TryGetObjectField(TEXT("job"), JobObject))
	{
		return false;
	}
	(*JobObject)->TryGetStringField(TEXT("status"), OutStatus);
	if (!(*JobObject)->TryGetStringField(TEXT("last_message"), OutMessage))
	{
		OutMessage.Empty();
	}
	return true;
}

bool IsTerminalStatus(const FString& Status)
{
	return Status == TEXT("succeeded") || Status == TEXT("failed") || Status == TEXT("cancelled");
}
}

void SLazyDeckDevicesPanel::Construct(const FArguments& InArgs)
{
	ChildSlot
		[SNew(SScrollBox) +
		 SScrollBox::Slot()
			 [SNew(SVerticalBox)

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(STextBlock).Text(this, &SLazyDeckDevicesPanel::GetStatusText)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("Connect", "Connect"))
																 .IsEnabled_Lambda([this] { return !IsBusy(); })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnConnectClicked)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(STextBlock).Text(LOCTEXT("DevicesLabel", "Configured devices (devices.toml)"))]

			  + SVerticalBox::Slot().AutoHeight().Padding(4).MaxHeight(
					150)[SAssignNew(DeviceListView, SListView<TSharedPtr<FLazyDeckDeviceRow>>)
							 .ListItemsSource(&Devices)
							 .SelectionMode(ESelectionMode::Single)
							 .OnSelectionChanged_Lambda([this](TSharedPtr<FLazyDeckDeviceRow> Item, ESelectInfo::Type) { SelectedDevice = Item; })
							 .OnGenerateRow_Lambda(
								 [](TSharedPtr<FLazyDeckDeviceRow> Item, const TSharedRef<STableViewBase>& OwnerTable)
								 {
									 return SNew(
										 STableRow<TSharedPtr<FLazyDeckDeviceRow>>,
										 OwnerTable)[SNew(STextBlock).Text(FText::FromString(FString::Printf(TEXT("%s (%s)"), *Item->Id, *Item->Machine)))];
								 })]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("PairSelected", "Pair selected device"))
																 .IsEnabled_Lambda([this] { return !IsBusy() && bConnected && SelectedDevice.IsValid(); })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnPairClicked)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("Discover", "Discover on LAN"))
																 .IsEnabled_Lambda([this] { return !IsBusy() && bConnected; })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnDiscoverClicked)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(STextBlock).Text(LOCTEXT("DeployLabel", "Deploy a build directory"))]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SAssignNew(GameIdBox, SEditableTextBox).HintText(LOCTEXT("GameIdHint", "Game ID"))]

			  + SVerticalBox::Slot().AutoHeight().Padding(
					4)[SNew(SHorizontalBox) +
					   SHorizontalBox::Slot().FillWidth(
						   1.0f)[SAssignNew(DeployDirBox, SEditableTextBox).HintText(LOCTEXT("DeployDirHint", "Absolute path to a staged/cooked build"))] +
					   SHorizontalBox::Slot().AutoWidth().Padding(
						   4, 0, 0, 0)[SNew(SButton).Text(LOCTEXT("Browse", "Browse...")).OnClicked(this, &SLazyDeckDevicesPanel::OnBrowseDeployDirClicked)]]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("Deploy", "Deploy"))
																 .IsEnabled_Lambda([this] { return !IsBusy() && bConnected && SelectedDevice.IsValid(); })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnDeployClicked)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(STextBlock).Text(LOCTEXT("LogsLabel", "Sync logs"))]

			  + SVerticalBox::Slot().AutoHeight().Padding(
					4)[SNew(SHorizontalBox) +
					   SHorizontalBox::Slot().FillWidth(
						   1.0f)[SAssignNew(LogsDirBox, SEditableTextBox).HintText(LOCTEXT("LogsDirHint", "Absolute local directory to sync logs into"))] +
					   SHorizontalBox::Slot().AutoWidth().Padding(
						   4, 0, 0, 0)[SNew(SButton).Text(LOCTEXT("Browse", "Browse...")).OnClicked(this, &SLazyDeckDevicesPanel::OnBrowseLogsDirClicked)]]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("SyncLogs", "Sync logs from selected device"))
																 .IsEnabled_Lambda([this] { return !IsBusy() && bConnected && SelectedDevice.IsValid(); })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnSyncLogsClicked)]

			  + SVerticalBox::Slot().AutoHeight().Padding(4)[SNew(SButton)
																 .Text(LOCTEXT("CancelJob", "Cancel current job"))
																 .IsEnabled_Lambda([this] { return bConnected && !CurrentJobId.IsEmpty(); })
																 .OnClicked(this, &SLazyDeckDevicesPanel::OnCancelJobClicked)]

			  +
			  SVerticalBox::Slot().AutoHeight().Padding(4).MaxHeight(200)[SAssignNew(LogBox, SMultiLineEditableTextBox).IsReadOnly(true).AutoWrapText(true)]]];

	Connect();
}

void SLazyDeckDevicesPanel::AppendLog(const FString& Line)
{
	LogText += Line + TEXT("\n");
	if (LogBox.IsValid())
	{
		LogBox->SetText(FText::FromString(LogText));
	}
}

FText SLazyDeckDevicesPanel::GetStatusText() const
{
	return FText::FromString(StatusText);
}

FReply SLazyDeckDevicesPanel::OnConnectClicked()
{
	Connect();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::Connect()
{
	if (bBusy)
	{
		return;
	}
	bBusy = true;
	bConnected = false;
	Client.Reset();
	Devices.Empty();
	SelectedDevice.Reset();
	CurrentJobId.Empty();
	if (DeviceListView.IsValid())
	{
		DeviceListView->RequestListRefresh();
	}

	FLazyDeckConnectionInfo Info;
	FString Error;
	if (FLazyDeckConnectionLocator::Load(FString(), Info, Error))
	{
		OnCapabilitiesResult(FLazyDeckApiResult(), Info);
		return;
	}

	const bool bStarted = FLazyDeckServerLauncher::StartIfNeeded([this](const FString& Line) { AppendLog(Line); });

	if (!bStarted)
	{
		StatusText = TEXT("Not connected");
		AppendLog(FString::Printf(TEXT("Could not find a running lazydeck serve: %s"), *Error));
		bBusy = false;
		return;
	}

	OnConnectAttempt(true, 20);
}

void SLazyDeckDevicesPanel::OnConnectAttempt(bool bAutoStarted, int32 RemainingAttempts)
{
	FLazyDeckConnectionInfo Info;
	FString Error;
	if (FLazyDeckConnectionLocator::Load(FString(), Info, Error))
	{
		OnCapabilitiesResult(FLazyDeckApiResult(), Info);
		return;
	}

	if (RemainingAttempts <= 0)
	{
		StatusText = TEXT("Not connected");
		AppendLog(FString::Printf(TEXT("Timed out waiting for lazydeck serve to start: %s"), *Error));
		bBusy = false;
		return;
	}

	FTimerHandle Unused;
	GEditor->GetTimerManager()->SetTimer(Unused, FTimerDelegate::CreateSP(this, &SLazyDeckDevicesPanel::OnConnectAttempt, bAutoStarted, RemainingAttempts - 1),
										 0.25f, false);
}

void SLazyDeckDevicesPanel::OnCapabilitiesResult(FLazyDeckApiResult /*Unused*/, FLazyDeckConnectionInfo Info)
{
	Connection = Info;
	const TSharedRef<FLazyDeckClient> NewClient = MakeShared<FLazyDeckClient>(Info);
	NewClient->GetCapabilities(FLazyDeckApiResultDelegate::CreateLambda(
		[this, NewClient, Info](FLazyDeckApiResult Result)
		{
			if (!Result.bOk)
			{
				StatusText = TEXT("Not connected");
				AppendLog(FString::Printf(TEXT("Found a connection file but the request failed: %s"), *Result.ErrorMessage));
				bBusy = false;
				return;
			}
			Client = NewClient;
			bConnected = true;
			StatusText = FString::Printf(TEXT("Connected: %s (pid %d, port %d)"), *Info.ApiVersion, Info.Pid, Info.Port);
			AppendLog(FString::Printf(TEXT("Connected to lazydeck serve at %s"), *Info.BaseUrl));
			RefreshDevices();
		}));
}

void SLazyDeckDevicesPanel::RefreshDevices()
{
	if (!Client.IsValid())
	{
		bBusy = false;
		return;
	}
	Client->ListDevices(FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::OnDevicesResult));
}

void SLazyDeckDevicesPanel::OnDevicesResult(FLazyDeckApiResult Result)
{
	bBusy = false;
	if (!Result.bOk)
	{
		AppendLog(FString::Printf(TEXT("Failed to list devices: %s"), *Result.ErrorMessage));
		return;
	}

	Devices.Empty();
	TSharedPtr<FJsonObject> JsonObject;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Result.Body);
	if (FJsonSerializer::Deserialize(Reader, JsonObject) && JsonObject.IsValid())
	{
		const TArray<TSharedPtr<FJsonValue>>* DeviceArray;
		if (JsonObject->TryGetArrayField(TEXT("devices"), DeviceArray))
		{
			for (const TSharedPtr<FJsonValue>& Value : *DeviceArray)
			{
				const TSharedPtr<FJsonObject>* DeviceObject;
				if (Value->TryGetObject(DeviceObject))
				{
					TSharedPtr<FLazyDeckDeviceRow> Row = MakeShared<FLazyDeckDeviceRow>();
					(*DeviceObject)->TryGetStringField(TEXT("id"), Row->Id);
					(*DeviceObject)->TryGetStringField(TEXT("machine"), Row->Machine);
					(*DeviceObject)->TryGetStringField(TEXT("login"), Row->Login);
					Devices.Add(Row);
				}
			}
		}
	}
	if (DeviceListView.IsValid())
	{
		DeviceListView->RequestListRefresh();
	}
}

FReply SLazyDeckDevicesPanel::OnDiscoverClicked()
{
	Discover();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::Discover()
{
	if (!Client.IsValid() || bBusy)
	{
		return;
	}
	bBusy = true;
	AppendLog(TEXT("Discovering devkits on the LAN..."));
	Client->DiscoverDevices(5.0f, FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::OnDiscoverResult));
}

void SLazyDeckDevicesPanel::OnDiscoverResult(FLazyDeckApiResult Result)
{
	bBusy = false;
	if (!Result.bOk)
	{
		AppendLog(FString::Printf(TEXT("Discover failed: %s"), *Result.ErrorMessage));
		return;
	}

	TSharedPtr<FJsonObject> JsonObject;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Result.Body);
	int32 Count = 0;
	if (FJsonSerializer::Deserialize(Reader, JsonObject) && JsonObject.IsValid())
	{
		const TArray<TSharedPtr<FJsonValue>>* DeviceArray;
		if (JsonObject->TryGetArrayField(TEXT("devices"), DeviceArray))
		{
			for (const TSharedPtr<FJsonValue>& Value : *DeviceArray)
			{
				const TSharedPtr<FJsonObject>* DeviceObject;
				if (Value->TryGetObject(DeviceObject))
				{
					FString Name, Address;
					(*DeviceObject)->TryGetStringField(TEXT("name"), Name);
					(*DeviceObject)->TryGetStringField(TEXT("address"), Address);
					AppendLog(FString::Printf(TEXT("Found %s @ %s"), *Name, *Address));
					++Count;
				}
			}
		}
	}
	if (Count == 0)
	{
		AppendLog(TEXT("No devkits found. Add a discovered device to devices.toml and restart lazydeck serve to pair it."));
	}
}

FReply SLazyDeckDevicesPanel::OnPairClicked()
{
	PairSelected();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::PairSelected()
{
	if (!Client.IsValid() || !SelectedDevice.IsValid() || bBusy)
	{
		return;
	}
	bBusy = true;
	const FString DeviceId = SelectedDevice->Id;
	AppendLog(FString::Printf(TEXT("Pairing %s..."), *DeviceId));
	Client->PairDevice(DeviceId, FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::OnPairResult, DeviceId));
}

void SLazyDeckDevicesPanel::OnPairResult(FLazyDeckApiResult Result, FString DeviceId)
{
	bBusy = false;
	AppendLog(Result.bOk ? FString::Printf(TEXT("Paired %s."), *DeviceId) : FString::Printf(TEXT("Failed to pair %s: %s"), *DeviceId, *Result.ErrorMessage));
}

FReply SLazyDeckDevicesPanel::OnDeployClicked()
{
	Deploy();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::Deploy()
{
	if (!Client.IsValid() || !SelectedDevice.IsValid() || bBusy)
	{
		return;
	}
	const FString GameId = GameIdBox.IsValid() ? GameIdBox->GetText().ToString().TrimStartAndEnd() : FString();
	const FString Directory = DeployDirBox.IsValid() ? DeployDirBox->GetText().ToString().TrimStartAndEnd() : FString();
	if (GameId.IsEmpty() || Directory.IsEmpty())
	{
		AppendLog(TEXT("Game ID and build directory are both required."));
		return;
	}
	if (FPaths::IsRelative(Directory))
	{
		AppendLog(TEXT("Build directory must be an absolute path."));
		return;
	}

	bBusy = true;
	const FString DeviceId = SelectedDevice->Id;
	AppendLog(FString::Printf(TEXT("Deploying %s to %s..."), *Directory, *DeviceId));
	Client->SubmitDeployment(DeviceId, GameId, Directory, /*bDeleteExtraneous=*/false,
							 FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::TrackJob, FString(TEXT("Deploy"))));
}

FReply SLazyDeckDevicesPanel::OnSyncLogsClicked()
{
	SyncLogs();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::SyncLogs()
{
	if (!Client.IsValid() || !SelectedDevice.IsValid() || bBusy)
	{
		return;
	}
	const FString Directory = LogsDirBox.IsValid() ? LogsDirBox->GetText().ToString().TrimStartAndEnd() : FString();
	if (Directory.IsEmpty())
	{
		AppendLog(TEXT("Local logs directory is required."));
		return;
	}
	if (FPaths::IsRelative(Directory))
	{
		AppendLog(TEXT("Local logs directory must be an absolute path."));
		return;
	}

	bBusy = true;
	const FString DeviceId = SelectedDevice->Id;
	AppendLog(FString::Printf(TEXT("Syncing logs from %s to %s..."), *DeviceId, *Directory));
	Client->SyncLogs(DeviceId, Directory, FString(), FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::TrackJob, FString(TEXT("Log sync"))));
}

void SLazyDeckDevicesPanel::TrackJob(FLazyDeckApiResult SubmitResult, FString Label)
{
	if (!SubmitResult.bOk)
	{
		bBusy = false;
		AppendLog(FString::Printf(TEXT("%s submission failed: %s"), *Label, *SubmitResult.ErrorMessage));
		return;
	}

	const FString JobId = ParseJobId(SubmitResult.Body);
	if (JobId.IsEmpty())
	{
		bBusy = false;
		AppendLog(FString::Printf(TEXT("%s was submitted but the response named no job to poll."), *Label));
		return;
	}

	CurrentJobId = JobId;
	AppendLog(FString::Printf(TEXT("%s job %s queued."), *Label, *JobId));
	PollJob(Label, JobId);
}

void SLazyDeckDevicesPanel::PollJob(const FString& Label, const FString& JobId)
{
	if (!Client.IsValid())
	{
		bBusy = false;
		return;
	}
	Client->GetJob(JobId, FLazyDeckApiResultDelegate::CreateSP(this, &SLazyDeckDevicesPanel::OnPollResult, Label, JobId));
}

void SLazyDeckDevicesPanel::OnPollResult(FLazyDeckApiResult Result, FString Label, FString JobId)
{
	if (!Result.bOk)
	{
		bBusy = false;
		AppendLog(FString::Printf(TEXT("Failed to poll job %s: %s"), *JobId, *Result.ErrorMessage));
		return;
	}

	FString Status, Message;
	if (!ParseJobStatus(Result.Body, Status, Message))
	{
		bBusy = false;
		AppendLog(FString::Printf(TEXT("Failed to parse the status of job %s."), *JobId));
		return;
	}

	if (IsTerminalStatus(Status))
	{
		bBusy = false;
		CurrentJobId.Empty();
		if (Status == TEXT("succeeded"))
		{
			AppendLog(FString::Printf(TEXT("%s complete."), *Label));
		}
		else
		{
			AppendLog(FString::Printf(TEXT("%s did not succeed (%s): %s"), *Label, *Status, *Message));
		}
		return;
	}

	// Still queued/running: check again shortly. Matches the Godot/Unity
	// clients' one-second poll cadence.
	FTimerHandle Unused;
	GEditor->GetTimerManager()->SetTimer(Unused, FTimerDelegate::CreateSP(this, &SLazyDeckDevicesPanel::PollJob, Label, JobId), 1.0f, false);
}

FReply SLazyDeckDevicesPanel::OnCancelJobClicked()
{
	CancelCurrentJob();
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::CancelCurrentJob()
{
	if (!Client.IsValid() || CurrentJobId.IsEmpty())
	{
		return;
	}
	const FString JobId = CurrentJobId;
	AppendLog(FString::Printf(TEXT("Cancelling job %s..."), *JobId));
	Client->CancelJob(JobId, FLazyDeckApiResultDelegate::CreateLambda(
								 [this, JobId](FLazyDeckApiResult Result)
								 {
									 if (!Result.bOk)
									 {
										 AppendLog(FString::Printf(TEXT("Failed to cancel job %s: %s"), *JobId, *Result.ErrorMessage));
										 return;
									 }
									 // If a poll loop is still watching this job it will observe the
									 // cancelled status on its next tick and clear CurrentJobId
									 // itself; if bBusy is already false, nothing else will, so
									 // retire it here rather than leaving Cancel lit forever.
									 if (!bBusy && CurrentJobId == JobId)
									 {
										 AppendLog(FString::Printf(TEXT("Job %s cancellation requested; no longer tracking it."), *JobId));
										 CurrentJobId.Empty();
									 }
								 }));
}

FReply SLazyDeckDevicesPanel::OnBrowseDeployDirClicked()
{
	BrowseForDirectory(DeployDirBox);
	return FReply::Handled();
}

FReply SLazyDeckDevicesPanel::OnBrowseLogsDirClicked()
{
	BrowseForDirectory(LogsDirBox);
	return FReply::Handled();
}

void SLazyDeckDevicesPanel::BrowseForDirectory(TSharedPtr<SEditableTextBox> TargetBox)
{
	if (!TargetBox.IsValid())
	{
		return;
	}
	IDesktopPlatform* DesktopPlatform = FDesktopPlatformModule::Get();
	if (!DesktopPlatform)
	{
		return;
	}
	const void* ParentWindowHandle = FSlateApplication::Get().FindBestParentWindowHandleForDialogs(nullptr);
	FString ChosenDirectory;
	if (DesktopPlatform->OpenDirectoryDialog(ParentWindowHandle, TEXT("Choose a directory"), TEXT(""), ChosenDirectory))
	{
		TargetBox->SetText(FText::FromString(ChosenDirectory));
	}
}

#undef LOCTEXT_NAMESPACE
