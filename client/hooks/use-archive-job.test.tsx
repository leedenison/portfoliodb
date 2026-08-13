import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { queryWrapper } from "@/test-utils";
import { JobStatus } from "@/gen/api/v1/api_pb";
import type { GetJobResult, JobSummary, ListJobsResult } from "@/lib/portfolio-api";
import { getJob, listJobs } from "@/lib/portfolio-api";
import { useArchiveJob } from "./use-archive-job";

// The session gate on useAuthedQuery: authenticated, so the queries run.
vi.mock("@/contexts/auth-context", () => ({
  useAuth: () => ({ state: { status: "authenticated" } }),
}));

vi.mock("@/lib/portfolio-api", () => ({
  listJobs: vi.fn(),
  getJob: vi.fn(),
}));

const mockListJobs = vi.mocked(listJobs);
const mockGetJob = vi.mocked(getJob);

function summary(id: string): JobSummary {
  return {
    id,
    jobType: "user_archive",
    filename: "user-archive.json",
    broker: "",
    status: JobStatus.SUCCESS,
    validationErrorCount: 0,
    identificationErrorCount: 0,
  };
}

function listing(...ids: string[]): ListJobsResult {
  return { jobs: ids.map(summary), nextPageToken: null, totalCount: ids.length };
}

function job(status: JobStatus): GetJobResult {
  return {
    status,
    validationErrors: [],
    identificationErrors: [],
    totalCount: 0,
    processedCount: 0,
    parts: [],
  };
}

describe("useArchiveJob", () => {
  beforeEach(() => {
    mockListJobs.mockReset();
    mockGetJob.mockReset();
  });

  it("watches the most recent job of its own type when none was started here", async () => {
    mockListJobs.mockResolvedValue(listing("newest", "older"));
    mockGetJob.mockResolvedValue(job(JobStatus.SUCCESS));

    const { result } = renderHook(() => useArchiveJob("user_archive", () => {}), {
      wrapper: queryWrapper(),
    });

    await waitFor(() => expect(result.current.job).toBeDefined());
    // The listing is asked for by type, so the admin page and the user page do
    // not adopt each other's imports.
    expect(mockListJobs).toHaveBeenCalledWith(null, "user_archive");
    expect(mockGetJob).toHaveBeenCalledWith("newest");
    expect(result.current.running).toBe(false);
  });

  it("watches the job it started instead of the one it found", async () => {
    mockListJobs.mockResolvedValue(listing("found"));
    mockGetJob.mockResolvedValue(job(JobStatus.RUNNING));

    const { result } = renderHook(() => useArchiveJob("user_archive", () => {}), {
      wrapper: queryWrapper(),
    });
    await waitFor(() => expect(result.current.job).toBeDefined());

    act(() => result.current.start("started"));

    await waitFor(() => expect(mockGetJob).toHaveBeenCalledWith("started"));
    // A job that has not reached a terminal status is still running, which is
    // what stops a second import being queued over it.
    await waitFor(() => expect(result.current.running).toBe(true));
  });

  it("reports a failed job as finished rather than running", async () => {
    mockListJobs.mockResolvedValue(listing("failed"));
    mockGetJob.mockResolvedValue(job(JobStatus.FAILED));

    const { result } = renderHook(() => useArchiveJob("user_archive", () => {}), {
      wrapper: queryWrapper(),
    });

    await waitFor(() => expect(result.current.job).toBeDefined());
    expect(result.current.running).toBe(false);
  });

  it("calls onFinished once per finished job, not once per render", async () => {
    mockListJobs.mockResolvedValue(listing("done"));
    mockGetJob.mockResolvedValue(job(JobStatus.SUCCESS));

    const onFinished = vi.fn();
    // A fresh arrow each render, as a page passing an inline callback gives it.
    const { result, rerender } = renderHook(
      () => useArchiveJob("user_archive", () => onFinished()),
      { wrapper: queryWrapper() },
    );

    await waitFor(() => expect(onFinished).toHaveBeenCalledTimes(1));
    rerender();
    rerender();
    // Dropping every cached query the import could have touched is not free, so
    // it happens when a job finishes and not whenever the page re-renders.
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(result.current.running).toBe(false);
  });

  it("does not call onFinished while the job is still running", async () => {
    mockListJobs.mockResolvedValue(listing("running"));
    mockGetJob.mockResolvedValue(job(JobStatus.RUNNING));

    const onFinished = vi.fn();
    const { result } = renderHook(() => useArchiveJob("user_archive", onFinished), {
      wrapper: queryWrapper(),
    });

    await waitFor(() => expect(result.current.running).toBe(true));
    expect(onFinished).not.toHaveBeenCalled();
  });
});
