"use client";

import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { qk } from "@/lib/query-keys";
import { getJob, listJobs, type GetJobResult } from "@/lib/portfolio-api";
import { JobStatus } from "@/gen/api/v1/api_pb";

/**
 * The import job an archive page watches.
 *
 * An import is applied on the server, so it keeps running whether or not the
 * page is open and the job to watch is looked up rather than remembered:
 * closing the tab and coming back must show the run that is still going.
 * Polling stops at a terminal status rather than running forever behind an idle
 * page.
 *
 * Both archive pages share this because the sequence is the behaviour rather
 * than the decoration; only the job type they ask for and what they drop
 * afterwards differ. `onFinished` fires once per finished job, and each page
 * passes its own because the cached queries an import invalidates are the ones
 * its own section reads.
 */
export function useArchiveJob(
  jobType: string,
  onFinished: () => void,
): {
  job: GetJobResult | undefined;
  running: boolean;
  start: (jobId: string) => void;
} {
  const queryClient = useQueryClient();
  const [startedJobId, setStartedJobId] = useState<string | null>(null);

  const recent = useAuthedQuery({
    queryKey: qk.jobs(),
    queryFn: () => listJobs(null, jobType),
  });
  const jobId = startedJobId ?? recent.data?.jobs[0]?.id ?? null;

  const job = useAuthedQuery({
    queryKey: qk.job(jobId ?? ""),
    queryFn: () => getJob(jobId!),
    enabled: jobId !== null,
    refetchInterval: (query) => {
      const s = query.state.data?.status;
      return s === JobStatus.SUCCESS || s === JobStatus.FAILED ? false : 2000;
    },
  });

  const running =
    job.data !== undefined &&
    job.data.status !== JobStatus.SUCCESS &&
    job.data.status !== JobStatus.FAILED;

  // Held in a ref so that a caller passing an inline function does not make
  // every render look like another finished job.
  const finished = useRef(onFinished);
  useEffect(() => {
    finished.current = onFinished;
  }, [onFinished]);

  // Keyed on the terminal status so it runs once per finished job.
  const terminalJobId = job.data && !running ? jobId : null;
  useEffect(() => {
    if (terminalJobId === null) return;
    finished.current();
  }, [terminalJobId]);

  function start(id: string) {
    setStartedJobId(id);
    queryClient.invalidateQueries({ queryKey: qk.jobs() });
  }

  return { job: job.data, running, start };
}
