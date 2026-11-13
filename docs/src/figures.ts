// The step-by-step figures of the documentation. Each figure has lanes,
// one row each, and moments, one column each; steps holds the sentence that
// explains each moment. A cell is the state of a lane at a moment, and its
// tone gives its color.

export type Tone =
  | "plain" // a fact without a state, such as a scheduled time
  | "event" // something that happens: a firing, a signal, a request
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "interrupted"
  | "ended" // skipped, canceled, retried, abandoned or dropped
  | "off"; // the agent is not running, or the machine sleeps

export type Cell = string | { text: string; tone?: Tone } | null;

export interface Figure {
  title: string;
  moments: string[];
  lanes: { label: string; cells: Cell[] }[];
  steps: string[];
}

const ev = (text: string): Cell => ({ text, tone: "event" });
const queued = (text = "queued"): Cell => ({ text, tone: "queued" });
const running = (text = "running"): Cell => ({ text, tone: "running" });
const succeeded = (text = "succeeded"): Cell => ({ text, tone: "succeeded" });
const failed = (text = "failed"): Cell => ({ text, tone: "failed" });
const interrupted = (text = "interrupted"): Cell => ({ text, tone: "interrupted" });
const ended = (text: string): Cell => ({ text, tone: "ended" });
const off = (text: string): Cell => ({ text, tone: "off" });

// How one firing becomes a run, and how an after trigger follows it.
export const firingPath: Figure = {
  title: "A cron trigger fires backup, and notify follows the failed run",
  moments: ["1", "2", "3", "4", "5", "6", "7"],
  lanes: [
    { label: "cron trigger · backup", cells: [ev("fires")] },
    { label: "run 12 · backup", cells: [null, queued(), running("running: rsync"), failed("failed: exit 23")] },
    { label: "run 13 · notify", cells: [null, null, null, null, queued(), running("running: notify.sh"), succeeded()] },
  ],
  steps: [
    "At 3:00, the cron trigger of <code>backup</code> fires. Cron triggers run inside the agent, the long-running kickd process.",
    "The agent writes run 12 into the database with the status <code>queued</code>. From this moment, the firing survives a stop or a crash of the agent.",
    "The agent takes run 12 from the queue, within milliseconds. No other run of <code>backup</code> is running, so its <code>concurrency</code> lets the run start: the agent records it as <code>running</code> and starts <code>rsync</code> as a child process.",
    "<code>rsync</code> exits with code 23. The agent records run 12 as <code>failed</code>, with the exit code and the first 64 KB of the output.",
    "<code>notify</code> has an after trigger for the runs of <code>backup</code> that end as <code>failed</code>, so the agent writes run 13 of <code>notify</code> into the database. It marks run 12 as handed to the after triggers in the same transaction, so a crash neither loses nor repeats this firing.",
    "The agent takes run 13 from the queue, records it as <code>running</code>, and starts <code>./notify.sh</code>. The command finds the failed run in <code>KICKD_AFTER_RUN_ID</code> and <code>KICKD_AFTER_EXIT_CODE</code>.",
    "<code>./notify.sh</code> exits with code 0, and the agent records run 13 as <code>succeeded</code>.",
  ],
};

// concurrency: what a firing does while a run of its event is running.
export const concurrencySkip: Figure = {
  title: "concurrency: skip",
  moments: ["1", "2", "3", "4"],
  lanes: [
    { label: "run 1", cells: [running(), running(), running(), succeeded()] },
    { label: "run 2", cells: [null, ended("skipped")] },
    { label: "run 3", cells: [null, null, ended("skipped")] },
  ],
  steps: [
    "The event fires, and run 1 starts.",
    "The event fires again while run 1 is running. The agent records run 2 as <code>skipped</code>, with the reason <code>already_running</code>, and starts no command.",
    "A third firing is skipped in the same way, as run 3.",
    "Run 1 ends. <code>kickd show 1</code> displays the 2 firings that were skipped while it ran. The next firing starts a run again.",
  ],
};

export const concurrencyQueue: Figure = {
  title: "concurrency: queue",
  moments: ["1", "2", "3", "4", "5", "6"],
  lanes: [
    { label: "run 1", cells: [running(), running(), running(), succeeded()] },
    { label: "run 2", cells: [null, queued(), queued(), running(), succeeded()] },
    { label: "run 3", cells: [null, null, queued(), queued(), running(), succeeded()] },
  ],
  steps: [
    "The event fires, and run 1 starts.",
    "The event fires again while run 1 is running. Run 2 waits in the queue.",
    "A third firing waits in the queue too, as run 3, behind run 2.",
    "Run 1 ends, and run 2, the oldest waiting run, starts.",
    "Run 2 ends, and run 3 starts. The waiting runs of one event start one at a time, in the order of their firings.",
    "Run 3 ends. Up to 1000 runs of one event can wait; a firing beyond them is recorded as <code>dropped</code>.",
  ],
};

export const concurrencyParallel: Figure = {
  title: "concurrency: parallel",
  moments: ["1", "2", "3", "4"],
  lanes: [
    { label: "run 1", cells: [running(), running(), succeeded()] },
    { label: "run 2", cells: [null, running(), running(), succeeded()] },
  ],
  steps: [
    "The event fires, and run 1 starts.",
    "The event fires again while run 1 is running, and run 2 starts at once, next to run 1. kickd sets no limit on the number of runs of one event that run at the same time.",
    "Run 1 ends. Run 2 goes on.",
    "Run 2 ends.",
  ],
};

export const concurrencyReplace: Figure = {
  title: "concurrency: replace",
  moments: ["1", "2", "3", "4"],
  lanes: [
    { label: "run 1", cells: [running(), running("running, stopping"), running("running, stopping"), ended("canceled, replaced")] },
    { label: "run 2", cells: [null, queued(), ended("skipped, replaced")] },
    { label: "run 3", cells: [null, null, queued(), running()] },
  ],
  steps: [
    "The event fires, and run 1 starts, such as a server that runs until it is stopped.",
    "The event fires again. The agent stops run 1 as <code>kickd cancel</code> does: it runs the <code>stop_command</code> of the event, when the event has one, and then sends SIGTERM. Run 2 waits in the queue until run 1 has stopped.",
    "The event fires once more while run 1 is stopping. Only the newest firing starts after the stop, so the agent records run 2 as <code>skipped</code>, with the reason <code>replaced</code>, and run 3 waits instead.",
    "The command of run 1 exits. The agent records run 1 as <code>canceled</code>, with the reason <code>replaced</code>, and starts run 3.",
  ],
};

// on_interrupt: what the next start of the agent does with a run that a
// stop or a crash cut off.
export const interruptRerun: Figure = {
  title: "A clean stop, and on_interrupt: rerun",
  moments: ["1", "2", "3", "4", "5"],
  lanes: [
    { label: "agent", cells: [running("running"), ev("stops on SIGTERM"), off("not running"), running("starts"), running("running")] },
    { label: "run 7 · sync", cells: [running("running, attempt 1"), interrupted(), interrupted(), ended("retried")] },
    { label: "run 8 · sync", cells: [null, null, null, queued("queued, attempt 2"), running("running, attempt 2")] },
  ],
  steps: [
    "Run 7 of <code>sync</code> is running.",
    "The agent gets SIGTERM, from Ctrl+C or from its service manager. It stops the command of run 7 as a timeout does, and records the run as <code>interrupted</code>.",
    "The agent is not running. Run 7 waits in the database for the next start.",
    "The agent starts, and before it starts any run, it settles the runs that were cut off. <code>sync</code> has <code>on_interrupt: rerun</code>, so the agent records run 7 as <code>retried</code> and queues run 8: the same firing, with the same request ID and parameters, as attempt 2.",
    "Run 8 starts. The command finds <code>KICKD_ATTEMPT=2</code>, so it can skip the work that the first attempt finished. After <code>max_attempts</code> attempts, 3 by default, a firing is given up instead.",
  ],
};

export const interruptAbandon: Figure = {
  title: "A crash, and on_interrupt: abandon",
  moments: ["1", "2", "3", "4"],
  lanes: [
    { label: "agent", cells: [running("running"), ev("killed"), off("not running"), running("starts")] },
    { label: "run 7 · send-report", cells: [running("running"), running("running"), running("running"), ended("abandoned, agent_crashed")] },
  ],
  steps: [
    "Run 7 of <code>send-report</code> is running.",
    "The agent is killed, or the machine loses power. Nothing records the end of run 7, so the database still says <code>running</code>.",
    "The agent is not running. A run that is <code>running</code> while no agent runs can only have been cut off by a crash.",
    "The agent starts, and before it starts any run, it settles the runs that were cut off. <code>send-report</code> keeps the default <code>on_interrupt: abandon</code>, so the agent records run 7 as <code>abandoned</code>, with the reason <code>agent_crashed</code>, and does not run it again. This suits work that must not happen twice, such as sending an email.",
  ],
};

// missed: what a cron trigger does about times that passed while the
// machine slept.
export const missedRun: Figure = {
  title: "schedule: '0 3 * * *' with missed: run",
  moments: ["23:00", "3:00", "7:30", "7:31", "next day 3:00"],
  lanes: [
    { label: "machine", cells: [off("goes to sleep"), off("asleep"), ev("wakes"), "awake", "awake"] },
    { label: "cron trigger", cells: [null, "3:00 passes", ev("fires for 3:00"), null, ev("fires")] },
    { label: "run", cells: [null, null, running("running"), succeeded(), running("running")] },
  ],
  steps: [
    "The machine goes to sleep at 23:00, and the agent sleeps with it.",
    "The scheduled time, 3:00, passes while the machine sleeps, and nothing runs.",
    "The machine wakes at 7:30. The agent looks at the clock at least once a second, and finds 3:00 more than a minute behind it, so it counts 3:00 as missed. With <code>missed: run</code>, the event fires once, right away, with <code>KICKD_CRON_MISSED=1</code> and the missed time in <code>KICKD_CRON_SCHEDULED_AT</code>.",
    "The run ends. However many times were missed, the event fired once for them.",
    "The next day, the event fires at 3:00, on time.",
  ],
};

export const missedSkip: Figure = {
  title: "schedule: '0 3 * * *' with missed: skip",
  moments: ["23:00", "3:00", "7:30", "next day 3:00"],
  lanes: [
    { label: "machine", cells: [off("goes to sleep"), off("asleep"), ev("wakes"), "awake"] },
    { label: "cron trigger", cells: [null, "3:00 passes", "3:00 skipped", ev("fires")] },
    { label: "run", cells: [null, null, null, running("running")] },
  ],
  steps: [
    "The machine goes to sleep at 23:00, and the agent sleeps with it.",
    "The scheduled time, 3:00, passes while the machine sleeps, and nothing runs.",
    "The machine wakes at 7:30, and the agent finds 3:00 more than a minute behind it. With <code>missed: skip</code>, the event does not fire for it, and the agent logs <code>Missed schedule skipped</code>.",
    "The next day, the event fires at 3:00, on time.",
  ],
};
