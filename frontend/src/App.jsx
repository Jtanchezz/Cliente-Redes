import React, { useEffect, useMemo, useRef, useState } from 'react'

const defaultPort = 9999

const formatTime = (iso) => {
  if (!iso) return ''
  const dt = new Date(iso)
  return dt.toLocaleTimeString('es-MX', { hour: '2-digit', minute: '2-digit' })
}

const ackLabel = (ack) => {
  if (ack === 'received') return 'Confirmado por servidor'
  if (ack === 'timeout') return 'Sin confirmación'
  return 'Pendiente'
}

export default function App() {
  const [user, setUser] = useState('')
  const [serverHost, setServerHost] = useState('')
  const [serverPort, setServerPort] = useState(defaultPort)
  const [connected, setConnected] = useState(false)
  const [identified, setIdentified] = useState(false)
  const [status, setStatus] = useState('Sin conexión')
  const [messages, setMessages] = useState([])
  const [users, setUsers] = useState([])
  const [recipient, setRecipient] = useState('')
  const [text, setText] = useState('')
  const [lastError, setLastError] = useState('')
  const bottomRef = useRef(null)

  useEffect(() => {
    if (!connected) return
    const es = new EventSource('/api/events')

    const safeJson = (event) => {
      if (!event || !event.data) return null
      try {
        return JSON.parse(event.data)
      } catch {
        return null
      }
    }

    es.addEventListener('message', (event) => {
      const data = safeJson(event)
      if (!data) return
      setMessages((prev) => [
        ...prev,
        {
          id: data.id,
          from: data.from,
          to: data.to,
          text: data.text,
          direction: data.direction || 'in',
          ack: data.ack || null,
          at: data.at || new Date().toISOString()
        }
      ].slice(-200))
    })

    es.addEventListener('ack', (event) => {
      const data = safeJson(event)
      if (!data || !data.ref_id) return
      setMessages((prev) =>
        prev.map((msg) =>
          msg.id === data.ref_id
            ? { ...msg, ack: data.status || 'received' }
            : msg
        )
      )
    })

    es.addEventListener('users', (event) => {
      const data = safeJson(event)
      if (!data || !Array.isArray(data.users)) return
      setUsers(data.users)
    })

    es.addEventListener('status', (event) => {
      const data = safeJson(event)
      if (!data) return
      if (data.identified) setIdentified(true)
      if (data.server) {
        setStatus(`Conectado a ${data.server}${data.identified ? ' (identificado)' : ''}`)
      }
    })

    es.addEventListener('error', (event) => {
      const data = safeJson(event)
      if (!data) return
      setLastError(`${data.code || 'ERR'}: ${data.detail || 'fallo del protocolo'}`)
    })

    es.onerror = () => {
      setStatus('Conexión SSE inestable')
    }

    return () => {
      es.close()
    }
  }, [connected])

  useEffect(() => {
    if (!bottomRef.current) return
    bottomRef.current.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const onlineUsers = useMemo(() => users.filter((u) => u.online), [users])
  const offlineUsers = useMemo(() => users.filter((u) => !u.online), [users])

  const handleConnect = async (event) => {
    event.preventDefault()
    setLastError('')
    setIdentified(false)
    setStatus('Conectando...')
    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          user,
          serverHost,
          serverPort: Number(serverPort)
        })
      })
      const data = await res.json()
      if (!res.ok) {
        setStatus('Sin conexión')
        setLastError(data.error || 'No se pudo conectar')
        return
      }
      setConnected(true)
      setStatus(`Conectado a ${data.serverHost}:${data.serverPort}`)
    } catch (err) {
      setStatus('Sin conexión')
      setLastError('Error de red con el backend')
    }
  }

  const handleSend = async (event) => {
    event.preventDefault()
    if (!recipient || !text.trim()) return
    setLastError('')
    try {
      const res = await fetch('/api/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ to: recipient, text })
      })
      const data = await res.json()
      if (!res.ok) {
        setLastError(data.error || 'No se pudo enviar')
        return
      }
      setMessages((prev) => [
        ...prev,
        {
          id: data.id,
          from: user,
          to: recipient,
          text,
          direction: 'out',
          ack: 'pending',
          at: new Date().toISOString()
        }
      ].slice(-200))
      setText('')
    } catch (err) {
      setLastError('Error de red con el backend')
    }
  }

  const handleList = async () => {
    setLastError('')
    try {
      const res = await fetch('/api/list', { method: 'POST' })
      const data = await res.json()
      if (!res.ok) {
        setLastError(data.error || 'No se pudo solicitar usuarios')
      }
    } catch (err) {
      setLastError('Error de red con el backend')
    }
  }

  return (
    <div className="app">
      <header className="header">
        <div>
          <p className="eyebrow">UDP Chat</p>
          <h1>Cliente</h1>
          <p className="subtitle">Mensajes en tiempo real, sin historial, con confirmación del servidor.</p>
        </div>
        <div className="status">
          <span className={connected ? 'dot online' : 'dot'} />
          <span>{status}</span>
        </div>
      </header>

      <div className="grid">
        <section className="panel connect">
          <h2>Conexión</h2>
          <form onSubmit={handleConnect} className="form">
            <label>
              Usuario
              <input
                value={user}
                onChange={(e) => setUser(e.target.value)}
                placeholder="tu_nombre"
                required
              />
            </label>
            <label>
              Servidor
              <input
                value={serverHost}
                onChange={(e) => setServerHost(e.target.value)}
                placeholder="192.168.1.10"
                required
              />
            </label>
            <label>
              Puerto
              <input
                type="number"
                value={serverPort}
                onChange={(e) => setServerPort(e.target.value)}
                min="1"
                max="65535"
                required
              />
            </label>
            <button type="submit" className="primary">Conectar</button>
          </form>
          {connected && !identified && <p className="muted">Identificando...</p>}
          {lastError && <p className="error">{lastError}</p>}
        </section>

        <section className="panel users">
          <div className="panel-header">
            <h2>Usuarios</h2>
            <button className="ghost" type="button" onClick={handleList} disabled={!connected}>
              Actualizar
            </button>
          </div>
          <div className="users-list">
            <div>
              <p className="list-title">En línea</p>
              {onlineUsers.length === 0 && <p className="muted">Sin usuarios</p>}
              {onlineUsers.map((u) => (
                <button
                  key={`online-${u.name}`}
                  className="user-chip"
                  type="button"
                  onClick={() => setRecipient(u.name)}
                >
                  <span className="dot online" />
                  {u.name}
                </button>
              ))}
            </div>
            <div>
              <p className="list-title">Fuera de línea</p>
              {offlineUsers.length === 0 && <p className="muted">Sin usuarios</p>}
              {offlineUsers.map((u) => (
                <button
                  key={`offline-${u.name}`}
                  className="user-chip ghost"
                  type="button"
                  onClick={() => setRecipient(u.name)}
                >
                  <span className="dot" />
                  {u.name}
                </button>
              ))}
            </div>
          </div>
        </section>

        <section className="panel chat">
          <div className="panel-header">
            <h2>Chat</h2>
            <div className="recipient">
              <span>Para</span>
              <input
                value={recipient}
                onChange={(e) => setRecipient(e.target.value)}
                placeholder="destinatario"
              />
            </div>
          </div>
          <div className="messages">
            {messages.length === 0 && (
              <div className="empty">
                <p>Escribe un mensaje para iniciar.</p>
              </div>
            )}
            {messages.map((msg) => (
              <div key={msg.id} className={`message ${msg.direction}`}>
                <div className="bubble">
                  <p className="meta">
                    <span>{msg.direction === 'out' ? 'Tú' : msg.from}</span>
                    <span>{formatTime(msg.at)}</span>
                  </p>
                  <p className="text">{msg.text}</p>
                  {msg.direction === 'out' && (
                    <p className={`ack ${msg.ack || 'pending'}`}>
                      {ackLabel(msg.ack)}
                    </p>
                  )}
                </div>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
          <form onSubmit={handleSend} className="composer">
            <input
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="Escribe tu mensaje..."
              disabled={!connected}
            />
            <button type="submit" className="primary" disabled={!connected || !recipient}>
              Enviar
            </button>
          </form>
        </section>
      </div>
    </div>
  )
}
