# End-to-end tests

The end-to-end tests run the kickd executable the way its users do.
A test writes a config, starts `kickd run`, fires events through their triggers or with `kickd event`, and checks the runs that kickd records.

## Layout

Each OS has a package of its own, with its own harness and its own test data:

```
macos/     the tests on macOS: shell scripts, and kickd as a launchd service
linux/     the tests on Linux: shell scripts, and kickd as a systemd unit
windows/   the tests on Windows: PowerShell scripts and batch files, and kickd as a Windows service
```

In each package:

- `kickd_test.go` builds kickd and holds the harness: a home directory for each test, and helpers that run kickd and read its runs. `start` returns when the agent has loaded its config, while its webhook server and the watches of its file triggers can still be starting, so a test that sends a webhook request or changes a watched file right after the start uses `startListening` or `startWatching`, which also wait for the log records `Webhook server listening` and `File watch started`.
- `testdata/` holds a directory for each config, laid out as a home directory: the config file `.kickd/config.yaml`, and the scripts and files of its events next to it, as a user keeps them. A test copies one of these directories into a new home directory. kickd and its commands see that directory as the home directory of the user: `HOME` on macOS and Linux, and `USERPROFILE` on Windows.
- The tests on Windows run as an administrator, as the runners of GitHub Actions do, and kickd reads the config file of an administrator from the folder `kickd` of `ProgramData`. The folder of a test is also the `ProgramData` folder of its kickd, so the config file of a Windows test is `kickd\config.yaml`.
- In the configs of `testdata/cron` and `testdata/cron-missed`, `{{at "ZONE"}}` stands for a cron schedule 25 seconds after the test starts, in the time zone ZONE.
- kickd reads the events of every YAML file next to its config file, so the files that a test saves while kickd runs wait in the subdirectory `edits`: the files `edits/config.*.yaml` are edits of a `config.yaml`, which a test saves over it, and `edits/restore.yaml` is a file with events, which a test saves next to it.

## Running the tests

The tests build only with the tag `e2e`, and each package only on its own OS:

```sh
go test -tags e2e ./test/e2e/macos     # on macOS
go test -tags e2e ./test/e2e/linux     # on Linux
go test -tags e2e ./test/e2e/windows   # on Windows
```

A test whose program, such as Ruby or `rustc`, is not installed is skipped.
The upgrade tests download earlier releases of kickd from GitHub, and are skipped when the download fails.
With `KICKD_E2E_ALL=1`, these tests fail instead.

Some tests change the machine: they install kickd as a service, write to the folders of the system, and move the clock.
They need sudo on macOS and Linux, and an administrator on Windows.
They run only with `KICKD_MACHINE_TEST=1`, on a machine that can be thrown away, such as a runner of GitHub Actions.
Their names start with `TestService` or `TestClock`, and on Windows also with `TestReadme`:

```sh
KICKD_MACHINE_TEST=1 go test -tags e2e -run 'TestService|TestClock' ./test/e2e/linux
```

The tests whose webhook server listens on a fixed port run one at a time, and the other tests run in parallel.
GitHub Actions runs every test on macOS, Linux and Windows for every push to `main` and every pull request, with `KICKD_E2E_ALL=1`, and then the tests that change the machine.

## The tests

Each test checks one behavior, which its name says.
A ✓ marks the OSes that have the test.

### The README config

The config that `kickd init` writes, which the README shows, with the scripts of its events. On Windows, its events work in `C:\scripts` and `C:\app`, so these tests change the machine there.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestReadmeBuildRunsWhenASourceFileChanges` | ✓ | ✓ | ✓ |
| `TestReadmeDeployRunsOnAWebhookRequestWithTheToken` | ✓ | ✓ | ✓ |
| `TestReadmeNotifyRunsByHand` | ✓ | ✓ | ✓ |
| `TestReadmeNotifyRunsWhenTheBackupFails` | ✓ | ✓ | ✓ |
| `TestReadmeWebhookRefusesARequestWithoutTheToken` | ✓ | ✓ | ✓ |

### Cron triggers

One moment written in time zones with offsets of whole hours, half an hour and three quarters of an hour, and `missed` across a time when kickd is not running.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestCronFiresAtTheSameMomentInEveryTimeZone` | ✓ | ✓ | ✓ |
| `TestCronRunsATimeMissedWhileStopped` | ✓ | ✓ | ✓ |
| `TestCronSkipsATimeMissedWhileStoppedWithSkip` | ✓ | ✓ | ✓ |

### File triggers

Every kind of change, new directories, `include`, `exclude`, `changes`, `recursive` and the debounce. Each test also checks that the command of the run gets the changes in the variables `KICKD_FILE_PATH`, `KICKD_FILE_OP`, `KICKD_FILE_COUNT` and `KICKD_FILE_PATHS`, as its payload has them.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestFileAFileInANewDirectoryFiresTheEvent` | ✓ | ✓ | ✓ |
| `TestFileChangesChooseTheKindsOfChange` | ✓ | ✓ | ✓ |
| `TestFileChangesWithinTheDebounceFireOneRun` | ✓ | ✓ | ✓ |
| `TestFileCreatingADirectoryFiresTheEvent` | ✓ | ✓ | ✓ |
| `TestFileCreatingAFileFiresTheEvent` | ✓ | ✓ | ✓ |
| `TestFileExcludeLeavesOutFiles` | ✓ | ✓ | ✓ |
| `TestFileIncludeChoosesTheFiles` | ✓ | ✓ | ✓ |
| `TestFileRemovingAFileFiresTheEvent` | ✓ | ✓ | ✓ |
| `TestFileRenamingAFileFiresTheEvent` | ✓ | ✓ | ✓ |
| `TestFileWithoutRecursiveASubdirectoryDoesNotFire` | ✓ | ✓ | ✓ |
| `TestFileWritingAFileFiresTheEvent` | ✓ | ✓ | ✓ |

### Webhook triggers

Tokens, signatures with `secret`, `wait: true`, parameters from the query, `methods`, `max_body_bytes`, requests from web browsers and the request ID of the caller.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestWebhookAcceptsARequestSignedWithTheSecret` | ✓ | ✓ | ✓ |
| `TestWebhookKeepsTheRequestIDOfTheCaller` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesABodyOverMaxBodyBytes` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesAMethodThatIsNotListed` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesARequestFromAWebBrowser` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesARequestWithAWrongSignature` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesARequestWithAWrongToken` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesARequestWithoutASignature` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesARequestWithoutTheToken` | ✓ | ✓ | ✓ |
| `TestWebhookTakesAParameterFromTheQuery` | ✓ | ✓ | ✓ |
| `TestWebhookWaitAnswersWithTheFailureOfTheRun` | ✓ | ✓ | ✓ |
| `TestWebhookWaitAnswersWithTheOutputOfTheRun` | ✓ | ✓ | ✓ |

### After triggers

An event that follows the runs of another event.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAfterDoesNotFireForAStatusThatIsNotListed` | ✓ | ✓ | ✓ |
| `TestAfterFiresForARunThatKickdCancelCanceledWhileItWaited` | ✓ | ✓ | ✓ |
| `TestAfterFiresWhenTheFollowedRunFails` | ✓ | ✓ | ✓ |
| `TestAfterFiresWhenTheFollowedRunIsAbandoned` | ✓ | ✓ | ✓ |
| `TestAfterFiresWhenTheFollowedRunIsSkipped` | ✓ | ✓ | ✓ |
| `TestAfterFollowsAnEventThatAnAfterTriggerFired` | ✓ | ✓ | ✓ |

### Startup triggers

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestStartupDoesNotFireWhenTheConfigIsSaved` | ✓ | ✓ | ✓ |
| `TestStartupFiresAgainWhenKickdStartsAgain` | ✓ | ✓ | ✓ |
| `TestStartupFiresWhenKickdStarts` | ✓ | ✓ | ✓ |

### Wake triggers

A runner of the CI cannot sleep, so this test checks the machine while it is awake. The unit tests of `internal/trigger` check a sleep with a clock that the test moves.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestWakeDoesNotFireWhileTheMachineIsAwake` | ✓ | ✓ | ✓ |

### Starting the agent

One agent at a time on a database, and no command when a trigger cannot start.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestASecondAgentDoesNotStartOnTheDatabaseOfARunningOne` | ✓ | ✓ | ✓ |
| `TestAnAgentWhoseWebhookAddressIsInUseStartsNoCommand` | ✓ | ✓ | ✓ |

### Upgrades

kickd started on the database of an earlier release, in which that release left a run that ended, a run that a crash cut off, and a run that waits. Each test runs for v0.6.0, whose database has the schema version 4, and for v0.7.0, whose database has the version 5, as `kickd` of that release for the OS and the architecture of the machine leaves them.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestUpgradeKeepsTheRunsThatEndedInTheDatabaseOfAnEarlierRelease` | ✓ | ✓ | ✓ |
| `TestUpgradeRunsTheRunThatACrashCutOffInTheDatabaseOfAnEarlierReleaseAgain` | ✓ | ✓ | ✓ |
| `TestUpgradeRunsTheRunThatWaitedInTheDatabaseOfAnEarlierRelease` | ✓ | ✓ | ✓ |

### Runs

`concurrency`, `on_interrupt`, `timeout` and `kickd cancel`. Windows has no signal that asks a console program to stop, so there a crash cuts runs off, and a stop does only in a service: the Windows tests of a stop install kickd as a Windows service, stop it with `kickd service stop` and start it again, so they change the machine.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAbandonGivesUpARunCutOffByACrash` | ✓ | ✓ | ✓ |
| `TestAbandonGivesUpARunCutOffByAStop` | ✓ | ✓ |  |
| `TestCancelStopsTheCommand` | ✓ | ✓ | ✓ |
| `TestParallelRunsFiringsAtTheSameTime` | ✓ | ✓ | ✓ |
| `TestQueueRunsFiringsOneAfterAnother` | ✓ | ✓ | ✓ |
| `TestRerunRunsARunCutOffByACrashAgain` | ✓ | ✓ | ✓ |
| `TestRerunRunsARunCutOffByAStopAgain` | ✓ | ✓ |  |
| `TestServiceAbandonGivesUpARunCutOffByAStop` |  |  | ✓ |
| `TestServiceRerunRunsARunCutOffByAStopAgain` |  |  | ✓ |
| `TestSkipRunsOnlyTheFirstOfTheFiringsThatWaitedWhileKickdWasStopped` | ✓ | ✓ | ✓ |
| `TestSkipSkipsAFiringWhileTheEventRuns` | ✓ | ✓ | ✓ |
| `TestTimeoutStopsTheCommand` | ✓ | ✓ | ✓ |

### Reloading the config

A save of the config and SIGHUP, the runs that wait during a reload, and a change of `database.path`, which takes effect only when kickd starts again.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestARunWaitingInTheQueueRunsWithTheSavedConfig` | ✓ | ✓ | ✓ |
| `TestChangingTheDatabasePathKeepsTheOldDatabase` | ✓ | ✓ | ✓ |
| `TestSavingABrokenConfigKeepsThePreviousConfig` | ✓ | ✓ | ✓ |
| `TestSavingTheConfigAddsAnEvent` | ✓ | ✓ | ✓ |
| `TestSighupReloadsTheConfig` | ✓ | ✓ |  |

### Events in several files

The events of the YAML files next to the config file.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAfterFollowsAnEventOfAnotherFile` | ✓ | ✓ | ✓ |
| `TestAnEventOfAYmlFileRuns` | ✓ | ✓ | ✓ |
| `TestAnEventOfAnotherYAMLFileRuns` | ✓ | ✓ | ✓ |
| `TestCheckListsTheFilesWhoseEventsItReads` | ✓ | ✓ | ✓ |
| `TestCheckNamesAYAMLFileWithoutEvents` | ✓ | ✓ | ✓ |
| `TestCheckRejectsASettingsSectionOutsideTheConfig` | ✓ | ✓ | ✓ |
| `TestCheckRejectsAnEventThatTwoFilesDefine` | ✓ | ✓ | ✓ |
| `TestEventsListsTheFileOfEachEvent` | ✓ | ✓ | ✓ |
| `TestRemovingAFileWithEventsRemovesItsEvents` | ✓ | ✓ | ✓ |
| `TestSavingAFileWithEventsAddsItsEvents` | ✓ | ✓ | ✓ |

### Parameters

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAParameterThatTheFiringLacksGetsItsDefault` | ✓ | ✓ | ✓ |
| `TestEventPassesAParameterGivenAsKeyAndValue` | ✓ | ✓ | ✓ |
| `TestEventRefusesAFiringWithoutARequiredParameter` | ✓ | ✓ | ✓ |
| `TestEventRefusesAParameterThatTheEventDoesNotDeclare` | ✓ | ✓ | ✓ |
| `TestEventRefusesAnEventThatIsNotDefined` | ✓ | ✓ | ✓ |
| `TestEventTakesParametersFromJSONInData` | ✓ | ✓ | ✓ |

### Firing by hand

`kickd event`, `--wait` and `kickd cancel`, with the agent running and not running.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestARunFiredWhileTheAgentIsNotRunningRunsWhenItStarts` | ✓ | ✓ | ✓ |
| `TestCancelCancelsARunThatHasNotStarted` | ✓ | ✓ | ✓ |
| `TestEventWaitExitsWith124WhenTheTimeoutPasses` | ✓ | ✓ | ✓ |
| `TestEventWaitExitsWithOneWhenTheRunFails` | ✓ | ✓ | ✓ |
| `TestEventWaitExitsWithZeroWhenTheRunSucceeds` | ✓ | ✓ | ✓ |
| `TestEventWaitPrintsTheRunThatEnded` | ✓ | ✓ | ✓ |
| `TestEventWarnsWhenTheAgentIsNotRunning` | ✓ | ✓ | ✓ |

### The environment of commands

The layers that [Running commands](https://etak64n.github.io/kickd/guides/commands/) describes.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestACommandWithoutWorkdirRunsInTheDirectoryOfTheConfig` | ✓ | ✓ | ✓ |
| `TestTheEnvOfTheEventExpandsTheVariablesOfKickd` | ✓ | ✓ | ✓ |
| `TestTheEnvOfTheEventGivesAValueThatTheRunLacks` | ✓ | ✓ | ✓ |
| `TestTheParametersOfTheRunReplaceTheEnvOfTheEvent` | ✓ | ✓ | ✓ |

### The payload file

The JSON file at `KICKD_PAYLOAD_FILE`: a new one for each run, readable only by the user of kickd on macOS and Linux, and deleted when the command ends.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestARerunGetsANewPayloadFileWithTheNextAttempt` | ✓ | ✓ | ✓ |
| `TestOnlyTheUserOfKickdCanReadThePayloadFile` | ✓ | ✓ |  |
| `TestThePayloadFileIsDeletedWhenTheCommandEnds` | ✓ | ✓ | ✓ |

### Programs in many languages

Each language has an event whose program prints the variables and the payload of its run, writes to standard error, and exits with the code of a parameter. The Japanese test puts Japanese and spaces in the folder, the name of the script and the parameter. On Windows, Perl cannot open a script with a Japanese name.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestProgramsInEachLanguageFailTheRunWithTheirExitCode` | ✓ | ✓ | ✓ |
| `TestProgramsInEachLanguageGetTheValuesOfTheirRun` | ✓ | ✓ | ✓ |
| `TestProgramsInEachLanguageHandleJapaneseAndSpaces` | ✓ | ✓ | ✓ |

### Long-running commands

A server that ignores SIGTERM, with `env` in the config file, `stop_command`, `concurrency: replace`, `restart: always` and an `output` file. Windows has no SIGTERM, so there the server is a PowerShell script that runs until its stop command asks it to stop, and the stop command waits until it has stopped, because kickd ends the process tree of the command once the stop command returns. A stop of kickd on Windows is a stop of the Windows service, so that test changes the machine.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestLongRunningCommandIsReplacedThroughItsStopCommand` | ✓ | ✓ | ✓ |
| `TestLongRunningCommandRestartsWhenItExits` | ✓ | ✓ | ✓ |
| `TestLongRunningCommandStopsWithKickdThroughItsStopCommand` | ✓ | ✓ | |
| `TestServiceStopsALongRunningCommandThroughItsStopCommand` |  |  | ✓ |

### The output of commands

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestShowCutsTheOutputAt64KB` | ✓ | ✓ | ✓ |
| `TestTheOutputKeepsTheLinesOfStandardOutputAndStandardErrorWhole` | ✓ | ✓ | ✓ |

### Child processes

Processes that a command leaves running, and the processes that a timeout or `kickd cancel` stops.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestABackgroundProcessInAWindowOfItsOwnKeepsRunning` |  |  | ✓ |
| `TestABackgroundProcessThatHoldsTheOutputFailsTheRunAfter10Seconds` | ✓ | ✓ | ✓ |
| `TestABackgroundProcessWithItsOutputRedirectedKeepsRunning` | ✓ | ✓ |  |
| `TestATimeoutStopsTheChildProcessesOfTheCommand` | ✓ | ✓ | ✓ |
| `TestCancelStopsTheChildProcessesOfTheCommand` | ✓ | ✓ | ✓ |

### The ways of running programs in the docs

The examples of [Running commands](https://etak64n.github.io/kickd/guides/commands/).

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAProgramIsFoundInThePathOfTheEvent` | ✓ | ✓ | ✓ |
| `TestAPythonScriptRunsInItsVirtualEnvironment` | ✓ | ✓ | ✓ |
| `TestCatPrintsThePayloadFromStandardInput` | ✓ | ✓ |  |
| `TestFindstrPrintsThePayloadFromStandardInput` |  |  | ✓ |
| `TestNodeReadsThePayloadOnStandardInput` | ✓ | ✓ | ✓ |
| `TestNpmRunRunsAScriptOfPackageJSON` | ✓ | ✓ | ✓ |
| `TestPythonReadsThePayloadOnStandardInput` | ✓ | ✓ | ✓ |

### The commands of kickd

The output of `kickd check`, `events`, `status`, `history`, `show` and `help`, and which config file kickd reads and writes.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAnUnknownCommandSaysThatHelpListsTheCommands` | ✓ | ✓ | ✓ |
| `TestCheckListsTheTriggersOfEachEvent` | ✓ | ✓ | ✓ |
| `TestCheckPrintsWhereTheLogAndTheDatabaseGo` | ✓ | ✓ | ✓ |
| `TestCheckRejectsAnEventWithoutTriggers` | ✓ | ✓ | ✓ |
| `TestCheckSaysThatWebhooksAreOffWhenWebhookEnabledIsFalse` | ✓ | ✓ | ✓ |
| `TestCommandsDoNotReadKickdYamlInTheCurrentDirectory` | ✓ | ✓ | ✓ |
| `TestCommandsOfAnAdministratorDoNotReadTheConfigInTheHomeFolder` |  |  | ✓ |
| `TestCommandsOfAnAdministratorReadTheConfigInProgramData` |  |  | ✓ |
| `TestCommandsReadTheConfigInTheKickdDirectoryOfTheHome` | ✓ | ✓ |  |
| `TestEventRefusesAnEventWithoutAManualTrigger` | ✓ | ✓ | ✓ |
| `TestEventsListsTheEventsWithTheirTriggers` | ✓ | ✓ | ✓ |
| `TestHelpListsTheCommands` | ✓ | ✓ | ✓ |
| `TestHelpOfACommandListsItsFlags` | ✓ | ✓ | ✓ |
| `TestHistoryFiltersTheRunsByStatus` | ✓ | ✓ | ✓ |
| `TestHistoryListsTheRunsThatWait` | ✓ | ✓ | ✓ |
| `TestInitDoesNotOverwriteAConfig` | ✓ | ✓ | ✓ |
| `TestInitOfAnAdministratorPutsTheLogAndTheDatabaseInProgramData` |  |  | ✓ |
| `TestInitOfAnAdministratorWritesTheConfigIntoProgramData` |  |  | ✓ |
| `TestInitPutsTheLogAndTheDatabaseInTheKickdDirectory` | ✓ | ✓ |  |
| `TestInitWritesAnEventsFileNextToTheConfig` | ✓ | ✓ | ✓ |
| `TestInitWritesTheConfigIntoTheKickdDirectoryOfTheHome` | ✓ | ✓ |  |
| `TestLicensesPrintTheLicenses` | ✓ | ✓ | ✓ |
| `TestLogLevelInTheEnvironmentOverridesTheConfig` | ✓ | ✓ | ✓ |
| `TestShowPrintsTheOutputOfARun` | ✓ | ✓ | ✓ |
| `TestStatusReportsAStoppedAgent` | ✓ | ✓ |  |
| `TestStatusReportsTheRunningAgent` | ✓ | ✓ | ✓ |
| `TestTheHelpFlagShowsTheHelpOfTheCommand` | ✓ | ✓ | ✓ |
| `TestVersionPrintsTheVersion` | ✓ | ✓ | ✓ |

### Services

kickd installed as a service as the installation guides do: launchd on macOS, systemd on Linux, and the Service Control Manager on Windows. The tests also run `kickd init` and `kickd check` with sudo, or as an administrator on Windows, on the config file for the whole machine. They change the machine.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestServiceAcceptsAWebhookRequest` |  |  | ✓ |
| `TestServiceCheckWithSudoReadsTheConfigForTheWholeMachine` | ✓ | ✓ |  |
| `TestServiceCheckWithSudoRefusesAConfigThatOtherUsersCanChange` | ✓ | ✓ |  |
| `TestServiceGivesCommandsTheProfileOfSystem` |  |  | ✓ |
| `TestServiceGivesCommandsTheTempFolderOfWindows` |  |  | ✓ |
| `TestServiceInitMakesTheConfigReadableOnlyByItsOwner` |  | ✓ |  |
| `TestServiceInitOfAnAdministratorWritesTheConfigForTheWholeMachine` |  |  | ✓ |
| `TestServiceInitWithSudoWritesTheConfigForTheWholeMachine` | ✓ | ✓ |  |
| `TestServiceInstallEnablesTheSystemUnitForMultiUserTarget` |  | ✓ |  |
| `TestServiceInstallEnablesTheUserUnitForDefaultTarget` |  | ✓ |  |
| `TestServiceInstallWritesALaunchAgentThatRunsKickdInTheKickdDirectory` | ✓ |  |  |
| `TestServiceInstallWritesAUnitThatRestartsKickdAfter5Seconds` |  | ✓ |  |
| `TestServiceOfTheSystemAcceptsAWebhookRequest` | ✓ | ✓ |  |
| `TestServiceOfTheSystemReloadsTheConfigOnSystemctlReload` |  | ✓ |  |
| `TestServiceOfTheSystemRunsCommandsAsRoot` | ✓ | ✓ |  |
| `TestServiceOfTheSystemRunsTheCommandOfAnEventAsItsUser` | ✓ | ✓ |  |
| `TestServiceOfTheSystemRunsCommandsWithThePathOfSystemd` |  | ✓ |  |
| `TestServiceOfTheSystemRunsCommandsWithoutHome` |  | ✓ |  |
| `TestServiceOfTheSystemStartsAgainAfterACrash` | ✓ | ✓ |  |
| `TestServiceOfTheSystemWritesTheLogAndTheDatabaseWhereTheConfigSays` | ✓ | ✓ |  |
| `TestServiceOfTheUserGivesCommandsTheHomeOfTheUser` | ✓ | ✓ |  |
| `TestServiceOfTheUserGivesCommandsTheRuntimeDirectoryOfTheUser` |  | ✓ |  |
| `TestServiceOfTheUserRunsCommandsAsTheUser` | ✓ | ✓ |  |
| `TestServiceOfTheUserRunsCommandsWithThePathOfEtcEnvironment` |  | ✓ |  |
| `TestServiceOfTheUserRunsCommandsWithThePathOfLaunchd` | ✓ |  |  |
| `TestServiceOfTheUserRunsCommandsWithoutLang` | ✓ |  |  |
| `TestServiceOfTheUserStartsAgainAfterACrash` | ✓ | ✓ |  |
| `TestServiceRunsCommandsAsSystem` |  |  | ✓ |
| `TestServiceStartsAgainAfterACrash` |  |  | ✓ |
| `TestServiceStatusReportsARunningService` | ✓ | ✓ | ✓ |
| `TestServiceUninstallDeletesTheService` |  |  | ✓ |
| `TestServiceUninstallRemovesTheLaunchAgent` | ✓ |  |  |
| `TestServiceUninstallRemovesTheUserUnit` |  | ✓ |  |
| `TestServiceWritesTheLogAndTheDatabaseWhereTheConfigSays` |  |  | ✓ |

### The clock

A move of the wall clock is not a sleep. These tests change the machine.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestClockMovingAheadDoesNotFireWake` | ✓ | ✓ | ✓ |
| `TestClockMovingBackDoesNotFireWake` | ✓ | ✓ | ✓ |
