import React, { useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { createEntry, deleteEntry, Entry, listEntries, openEntriesSocket, updateEntry } from './api';
import './style.css';

function formatDate(now = new Date()) {
  const weekdays = ['日', '一', '二', '三', '四', '五', '六'];
  return `${now.getFullYear()}年${now.getMonth() + 1}月${now.getDate()}日 周${weekdays[now.getDay()]}`;
}

function formatTime(ms: number) {
  const d = new Date(ms);
  return `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`;
}

function gapClass(previous: Entry | undefined, current: Entry) {
  if (!previous) return 'gap-0';
  const minutes = Math.max(0, Math.floor((current.created_at - previous.created_at) / 60000));
  const level = Math.min(5, Math.floor(minutes / 30));
  return `gap-${level}`;
}

function App() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [input, setInput] = useState('');
  const [status, setStatus] = useState('连接中');
  const [selected, setSelected] = useState<Entry | null>(null);
  const [editing, setEditing] = useState<Entry | null>(null);
  const [editText, setEditText] = useState('');
  const [bgUrl, setBgUrl] = useState(() => localStorage.getItem('life-db-bg') || '');
  const [opacity, setOpacity] = useState(() => Number(localStorage.getItem('life-db-opacity') || '0.88'));
  const longPressTimer = useRef<number | null>(null);

  const visibleEntries = useMemo(
    () => entries.filter((entry) => !entry.deleted_at).sort((a, b) => a.created_at - b.created_at),
    [entries],
  );

  async function refresh() {
    try {
      const next = await listEntries();
      setEntries(next);
      setStatus(`已同步 ${formatTime(Date.now())}`);
    } catch (error) {
      setStatus('连接失败');
      console.error(error);
    }
  }

  useEffect(() => {
    refresh();
    const socket = openEntriesSocket(refresh);
    socket.onopen = () => setStatus('实时连接');
    socket.onclose = () => setStatus('实时断开');
    return () => socket.close();
  }, []);

  useEffect(() => {
    localStorage.setItem('life-db-bg', bgUrl);
  }, [bgUrl]);

  useEffect(() => {
    localStorage.setItem('life-db-opacity', String(opacity));
  }, [opacity]);

  async function submit() {
    const text = input.trim();
    if (!text) return;
    setInput('');
    try {
      const created = await createEntry(text);
      setEntries((old) => [...old, created]);
      setStatus('已保存');
    } catch (error) {
      setInput(text);
      setStatus('保存失败');
      alert(String(error));
    }
  }

  function startLongPress(entry: Entry) {
    stopLongPress();
    longPressTimer.current = window.setTimeout(() => setSelected(entry), 450);
  }

  function stopLongPress() {
    if (longPressTimer.current !== null) {
      window.clearTimeout(longPressTimer.current);
      longPressTimer.current = null;
    }
  }

  function openEdit(entry: Entry) {
    setSelected(null);
    setEditing(entry);
    setEditText(entry.content);
  }

  async function confirmEdit() {
    if (!editing) return;
    try {
      const updated = await updateEntry(editing, editText.trim());
      setEntries((old) => old.map((entry) => (entry.id === updated.id ? updated : entry)));
      setEditing(null);
      setStatus('已更新');
    } catch (error) {
      alert(String(error));
      refresh();
    }
  }

  async function confirmDelete(entry: Entry) {
    setSelected(null);
    if (!window.confirm('删除这条记录？')) return;
    try {
      const deleted = await deleteEntry(entry);
      setEntries((old) => old.map((item) => (item.id === deleted.id ? deleted : item)));
      setStatus('已删除');
    } catch (error) {
      alert(String(error));
      refresh();
    }
  }

  return (
    <main
      className="app"
      style={{
        backgroundImage: bgUrl ? `url(${bgUrl})` : undefined,
      }}
    >
      <section className="panel" style={{ backgroundColor: `rgba(253, 253, 251, ${opacity})` }}>
        <header className="header">
          <div className="date">{formatDate()}</div>
          <div className="status">{status}</div>
          <div className="settings">
            <input
              value={bgUrl}
              onChange={(event) => setBgUrl(event.target.value)}
              placeholder="背景图片 URL，可留空"
            />
            <label>
              透明度
              <input
                type="range"
                min="0.25"
                max="1"
                step="0.01"
                value={opacity}
                onChange={(event) => setOpacity(Number(event.target.value))}
              />
            </label>
          </div>
        </header>

        <div className="timeline">
          {visibleEntries.map((entry, index) => (
            <article
              key={entry.id}
              className={`entry ${gapClass(visibleEntries[index - 1], entry)}`}
              onPointerDown={() => startLongPress(entry)}
              onPointerUp={stopLongPress}
              onPointerLeave={stopLongPress}
              onContextMenu={(event) => {
                event.preventDefault();
                setSelected(entry);
              }}
            >
              <time>{formatTime(entry.created_at)}</time>
              <p>{entry.content}</p>
            </article>
          ))}
        </div>

        <footer className="inputBar">
          <input
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) submit();
            }}
            placeholder="深呼吸，说点什么..."
            autoFocus
          />
          <button onClick={submit}>+</button>
        </footer>
      </section>

      {selected && (
        <div className="modalBackdrop" onClick={() => setSelected(null)}>
          <div className="menu" onClick={(event) => event.stopPropagation()}>
            <div className="menuTitle">{formatTime(selected.created_at)} · {selected.content}</div>
            <button onClick={() => openEdit(selected)}>编辑</button>
            <button className="danger" onClick={() => confirmDelete(selected)}>删除</button>
            <button onClick={() => setSelected(null)}>取消</button>
          </div>
        </div>
      )}

      {editing && (
        <div className="modalBackdrop" onClick={() => setEditing(null)}>
          <div className="editDialog" onClick={(event) => event.stopPropagation()}>
            <textarea value={editText} onChange={(event) => setEditText(event.target.value)} autoFocus />
            <div className="dialogActions">
              <button onClick={() => setEditing(null)}>取消</button>
              <button onClick={confirmEdit}>保存</button>
            </div>
          </div>
        </div>
      )}
    </main>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
