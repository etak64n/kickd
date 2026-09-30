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

- `kickd_test.go` builds kickd and holds the harness: a home directory for each test, and helpers that run kickd and read its runs.
- `testdata/` holds a directory for each config: `kickd.yaml`, and the scripts and files of its events, as a user keeps them. A test copies one of these directories into a new home directory. kickd and its commands see that directory as the home directory of the user: `HOME` on macOS and Linux, and `USERPROFILE` on Windows.
- In the configs of `testdata/cron` and `testdata/cron-missed`, `{{at "ZONE"}}` stands for a cron schedule 25 seconds after the test starts, in the time zone ZONE.
- kickd reads the events of every YAML file next to its config file, so the files that a test saves while kickd runs wait in the subdirectory `edits`: `edits/kickd.edited.yaml` and `edits/kickd.broken.yaml` are edits of a `kickd.yaml`, which a test saves over it, and `edits/restore.yaml` is a file with events, which a test saves next to it.

## Running the tests

The tests build only with the tag `e2e`, and each package only on its own OS:

```sh
go test -tags e2e ./test/e2e/macos     # on macOS
go test -tags e2e ./test/e2e/linux     # on Linux
go test -tags e2e ./test/e2e/windows   # on Windows
```

A test whose program, such as Ruby or `rustc`, is not installed is skipped.
With `KICKD_E2E_ALL=1`, it fails instead.

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

Every kind of change, new directories, `include`, `exclude`, `changes`, `recursive` and the debounce.

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

Tokens, signatures with `secret`, `wait: true`, parameters from the query, `methods`, `max_body_bytes` and the request ID of the caller.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestWebhookAcceptsARequestSignedWithTheSecret` | ✓ | ✓ | ✓ |
| `TestWebhookKeepsTheRequestIDOfTheCaller` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesABodyOverMaxBodyBytes` | ✓ | ✓ | ✓ |
| `TestWebhookRefusesAMethodThatIsNotListed` | ✓ | ✓ | ✓ |
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

### Runs

`concurrency`, `on_interrupt`, `timeout` and `kickd cancel`. Windows has no signal that asks a console program to stop, so there a crash cuts runs off, and a stop does only in a service.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestAbandonGivesUpARunCutOffByACrash` | ✓ | ✓ | ✓ |
| `TestAbandonGivesUpARunCutOffByAStop` | ✓ | ✓ |  |
| `TestCancelStopsTheCommand` | ✓ | ✓ | ✓ |
| `TestParallelRunsFiringsAtTheSameTime` | ✓ | ✓ | ✓ |
| `TestQueueRunsFiringsOneAfterAnother` | ✓ | ✓ | ✓ |
| `TestRerunRunsARunCutOffByACrashAgain` | ✓ | ✓ | ✓ |
| `TestRerunRunsARunCutOffByAStopAgain` | ✓ | ✓ |  |
| `TestSkipSkipsAFiringWhileTheEventRuns` | ✓ | ✓ | ✓ |
| `TestTimeoutStopsTheCommand` | ✓ | ✓ | ✓ |

### Reloading the config

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
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

The layers that [Running commands](../../docs/commands.md) describes.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestACommandWithoutWorkdirRunsInTheDirectoryOfTheConfig` | ✓ | ✓ | ✓ |
| `TestTheEnvOfTheEventExpandsTheVariablesOfKickd` | ✓ | ✓ | ✓ |
| `TestTheEnvOfTheEventGivesAValueThatTheRunLacks` | ✓ | ✓ | ✓ |
| `TestTheParametersOfTheRunReplaceTheEnvOfTheEvent` | ✓ | ✓ | ✓ |

### Programs in many languages

Each language has an event whose program prints the variables and the payload of its run, writes to standard error, and exits with the code of a parameter. The Japanese test puts Japanese and spaces in the folder, the name of the script and the parameter. On Windows, Perl cannot open a script with a Japanese name.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestProgramsInEachLanguageFailTheRunWithTheirExitCode` | ✓ | ✓ | ✓ |
| `TestProgramsInEachLanguageGetTheValuesOfTheirRun` | ✓ | ✓ | ✓ |
| `TestProgramsInEachLanguageHandleJapaneseAndSpaces` | ✓ | ✓ | ✓ |

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

The examples of [Running commands](../../docs/commands.md).

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

The output of `kickd check`, `events`, `status`, `queue`, `runs` and `show`, and where kickd finds and writes its config.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestCheckListsTheTriggersOfEachEvent` | ✓ | ✓ | ✓ |
| `TestCheckPrintsWhereTheLogAndTheDatabaseGo` | ✓ | ✓ | ✓ |
| `TestCheckRejectsAnEventWithoutTriggers` | ✓ | ✓ | ✓ |
| `TestCheckSaysThatWebhooksAreOffWhenWebhookEnabledIsFalse` | ✓ | ✓ | ✓ |
| `TestCommandsFindKickdYamlInTheCurrentDirectory` | ✓ | ✓ | ✓ |
| `TestEventRefusesAnEventWithoutAManualTrigger` | ✓ | ✓ | ✓ |
| `TestEventsListsTheEventsWithTheirTriggers` | ✓ | ✓ | ✓ |
| `TestInitDoesNotOverwriteAConfig` | ✓ | ✓ | ✓ |
| `TestInitPutsTheLogAndTheDatabaseInTheKickdDirectory` | ✓ | ✓ | ✓ |
| `TestInitWritesAnEventsFileNextToTheConfig` | ✓ | ✓ | ✓ |
| `TestInitWritesTheConfigIntoTheKickdDirectoryOfTheHome` | ✓ | ✓ | ✓ |
| `TestKickdConfigNamesTheConfig` | ✓ | ✓ | ✓ |
| `TestLicensesPrintTheLicenses` | ✓ | ✓ | ✓ |
| `TestLogLevelInTheEnvironmentOverridesTheConfig` | ✓ | ✓ | ✓ |
| `TestQueueShowsTheRunsThatWait` | ✓ | ✓ | ✓ |
| `TestRunsFiltersTheHistoryByStatus` | ✓ | ✓ | ✓ |
| `TestShowPrintsTheOutputOfARun` | ✓ | ✓ | ✓ |
| `TestStatusReportsAStoppedAgent` | ✓ | ✓ |  |
| `TestStatusReportsTheRunningAgent` | ✓ | ✓ | ✓ |
| `TestTheConfigOfTheUserComesBeforeKickdYaml` | ✓ | ✓ | ✓ |
| `TestVersionPrintsTheVersion` | ✓ | ✓ | ✓ |

### Services

kickd installed as a service as the installation guides do: launchd on macOS, systemd on Linux, and the Service Control Manager on Windows. They change the machine.

| Test | macOS | Linux | Windows |
|---|:-:|:-:|:-:|
| `TestServiceAcceptsAWebhookRequest` |  |  | ✓ |
| `TestServiceGivesCommandsTheProfileOfSystem` |  |  | ✓ |
| `TestServiceGivesCommandsTheTempFolderOfWindows` |  |  | ✓ |
| `TestServiceInitMakesTheConfigReadableOnlyByItsOwner` |  | ✓ |  |
| `TestServiceInitWritesTheSystemPathsIntoAConfigOutsideTheHome` | ✓ | ✓ | ✓ |
| `TestServiceInstallEnablesTheSystemUnitForMultiUserTarget` |  | ✓ |  |
| `TestServiceInstallEnablesTheUserUnitForDefaultTarget` |  | ✓ |  |
| `TestServiceInstallWithUserInstallsTheServiceOfTheSystem` |  |  | ✓ |
| `TestServiceInstallWritesALaunchAgentWithTheExecutableAndTheConfig` | ✓ |  |  |
| `TestServiceInstallWritesAUnitThatRestartsKickdAfter5Seconds` |  | ✓ |  |
| `TestServiceOfTheSystemAcceptsAWebhookRequest` | ✓ | ✓ |  |
| `TestServiceOfTheSystemReloadsTheConfigOnSystemctlReload` |  | ✓ |  |
| `TestServiceOfTheSystemRunsCommandsAsRoot` | ✓ | ✓ |  |
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
