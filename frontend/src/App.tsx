import { useState, type FormEvent } from "react";
import { googleConnectUrl, useCreateGroup, useGoogleStatus, useGroups } from "./lib/queries";
import type { ApiError, Group } from "./lib/api";

const statusStyles: Record<string, string> = {
  draft: "bg-slate-100 text-slate-700 ring-slate-200",
  launching: "bg-blue-50 text-blue-700 ring-blue-200",
  launched: "bg-emerald-50 text-emerald-700 ring-emerald-200",
  partially_failed: "bg-amber-50 text-amber-700 ring-amber-200",
  failed: "bg-red-50 text-red-700 ring-red-200",
  archived: "bg-zinc-100 text-zinc-500 ring-zinc-200",
};

function StatusBadge({ status }: { status: string }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${statusStyles[status] ?? statusStyles.draft}`}
    >
      {status.replace("_", " ")}
    </span>
  );
}

function CreateGroupForm() {
  const [name, setName] = useState("");
  const [courseName, setCourseName] = useState("");
  const [startDate, setStartDate] = useState("");
  const [apiError, setApiError] = useState<ApiError | null>(null);
  const create = useCreateGroup();

  const fieldError = (field: string) => apiError?.fieldErrors?.[field];

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setApiError(null);
    try {
      await create.mutateAsync({ name, courseName, startDate });
      setName("");
      setCourseName("");
      setStartDate("");
    } catch (err) {
      setApiError(err as ApiError);
    }
  }

  const inputClass = (field: string) =>
    `w-full rounded-md border px-3 py-2 text-sm outline-none focus:ring-2 ${
      fieldError(field)
        ? "border-red-300 focus:ring-red-200"
        : "border-slate-300 focus:ring-indigo-200"
    }`;

  return (
    <form
      onSubmit={onSubmit}
      className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm"
    >
      <h2 className="text-base font-semibold text-slate-900">New group</h2>
      <p className="mt-1 text-sm text-slate-500">
        Groups start as draft — Slack, Classroom, and Drive launch in one click later.
      </p>

      <div className="mt-4 grid gap-4 sm:grid-cols-3">
        <div>
          <label htmlFor="name" className="mb-1 block text-sm font-medium text-slate-700">
            Group name
          </label>
          <input
            id="name"
            className={inputClass("name")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Python Backend 13"
          />
          {fieldError("name") && <p className="mt-1 text-xs text-red-600">{fieldError("name")}</p>}
        </div>
        <div>
          <label htmlFor="courseName" className="mb-1 block text-sm font-medium text-slate-700">
            Course name
          </label>
          <input
            id="courseName"
            className={inputClass("courseName")}
            value={courseName}
            onChange={(e) => setCourseName(e.target.value)}
            placeholder="Full-Stack Python"
          />
          {fieldError("courseName") && (
            <p className="mt-1 text-xs text-red-600">{fieldError("courseName")}</p>
          )}
        </div>
        <div>
          <label htmlFor="startDate" className="mb-1 block text-sm font-medium text-slate-700">
            Start date
          </label>
          <input
            id="startDate"
            type="date"
            className={inputClass("startDate")}
            value={startDate}
            onChange={(e) => setStartDate(e.target.value)}
          />
          {fieldError("startDate") && (
            <p className="mt-1 text-xs text-red-600">{fieldError("startDate")}</p>
          )}
        </div>
      </div>

      {apiError && !apiError.fieldErrors && (
        <p className="mt-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
          {apiError.message}
        </p>
      )}

      <button
        type="submit"
        disabled={create.isPending}
        className="mt-4 rounded-md bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-50"
      >
        {create.isPending ? "Creating…" : "Create group"}
      </button>
    </form>
  );
}

function GroupRow({ group }: { group: Group }) {
  return (
    <tr className="border-t border-slate-100">
      <td className="px-4 py-3 font-medium text-slate-900">{group.name}</td>
      <td className="px-4 py-3 text-slate-600">{group.courseName}</td>
      <td className="px-4 py-3 text-slate-600">{group.startDate}</td>
      <td className="px-4 py-3">
        <StatusBadge status={group.status} />
      </td>
      <td className="px-4 py-3 text-slate-500">
        {new Date(group.createdAt).toLocaleDateString()}
      </td>
    </tr>
  );
}

function GoogleConnectionCard() {
  const { data, isPending, isError } = useGoogleStatus();

  let badge = "bg-slate-100 text-slate-700 ring-slate-200";
  let label = "unknown";
  if (data) {
    label = data.status.replace("_", " ");
    if (data.status === "connected") badge = "bg-emerald-50 text-emerald-700 ring-emerald-200";
    else if (data.status === "error") badge = "bg-red-50 text-red-700 ring-red-200";
  }

  return (
    <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-base font-semibold text-slate-900">Integrations</h2>
          <p className="mt-1 text-sm text-slate-500">
            {data?.accountEmail
              ? `Google · ${data.accountEmail}`
              : "Connect the organization's Google account (Classroom, Drive, Forms)."}
          </p>
        </div>
        {!isPending && !isError && (
          <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${badge}`}>
            {label}
          </span>
        )}
      </div>
      {isError && (
        <p className="mt-3 text-sm text-slate-500">
          Google OAuth is not configured on the server.
        </p>
      )}
      {data?.status !== "connected" && !isError && !isPending && (
        <a
          href={googleConnectUrl()}
          className="mt-4 inline-block rounded-md bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-500"
        >
          Connect Google
        </a>
      )}
    </section>
  );
}

export default function App() {
  const { data, isPending, isError } = useGroups();

  return (
    <div className="min-h-screen bg-slate-50">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto max-w-4xl px-6 py-5">
          <h1 className="text-lg font-bold text-slate-900">Group Launchpad</h1>
          <p className="text-sm text-slate-500">
            One form. One launch action. Every platform synchronized.
          </p>
        </div>
      </header>

      <main className="mx-auto max-w-4xl space-y-6 px-6 py-8">
        <GoogleConnectionCard />
        <CreateGroupForm />

        <section className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <div className="flex items-center justify-between px-6 py-4">
            <h2 className="text-base font-semibold text-slate-900">Groups</h2>
            {data && (
              <span className="text-sm text-slate-500">{data.page.total} total</span>
            )}
          </div>

          {isPending && <p className="px-6 pb-6 text-sm text-slate-500">Loading groups…</p>}

          {isError && (
            <p className="px-6 pb-6 text-sm text-red-600">
              Could not load groups — is the API running on :8080?
            </p>
          )}

          {data && data.data.length === 0 && (
            <p className="px-6 pb-6 text-sm text-slate-500">
              No groups yet. Create the first one above.
            </p>
          )}

          {data && data.data.length > 0 && (
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-t border-slate-100 bg-slate-50/50 text-xs uppercase tracking-wide text-slate-500">
                  <th className="px-4 py-2 font-semibold">Group</th>
                  <th className="px-4 py-2 font-semibold">Course</th>
                  <th className="px-4 py-2 font-semibold">Starts</th>
                  <th className="px-4 py-2 font-semibold">Status</th>
                  <th className="px-4 py-2 font-semibold">Created</th>
                </tr>
              </thead>
              <tbody>
                {data.data.map((group) => (
                  <GroupRow key={group.id} group={group} />
                ))}
              </tbody>
            </table>
          )}
        </section>
      </main>
    </div>
  );
}
