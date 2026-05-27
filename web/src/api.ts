export type Entry = {
  id: string;
  content: string;
  created_at: number;
  updated_at: number;
  deleted_at?: number | null;
  version: number;
  source_device_id: string;
};

export async function listEntries(): Promise<Entry[]> {
  const res = await fetch('/api/entries');
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export async function createEntry(content: string): Promise<Entry> {
  const res = await fetch('/api/entries', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content, source_device_id: 'web' }),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export async function updateEntry(entry: Entry, content: string): Promise<Entry> {
  const res = await fetch(`/api/entries/${entry.id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content, version: entry.version }),
  });
  if (res.status === 409) throw new Error('内容已被其他端修改，请刷新后再编辑');
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export async function deleteEntry(entry: Entry): Promise<Entry> {
  const res = await fetch(`/api/entries/${entry.id}?version=${entry.version}`, {
    method: 'DELETE',
  });
  if (res.status === 409) throw new Error('内容已被其他端修改，请刷新后再删除');
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export function openEntriesSocket(onChange: () => void): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${protocol}//${window.location.host}/ws`);
  socket.onmessage = () => onChange();
  return socket;
}
