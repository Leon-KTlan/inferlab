import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import PublicReport from './PublicReport'
import BetaPages, { BetaLinks } from './BetaPages'
import './styles.css'

const share = /^\/share\/([A-Za-z0-9_-]+)\/?$/.exec(window.location.pathname)
const path = window.location.pathname.replace(/\/$/, '')
createRoot(document.getElementById('root')!).render(<StrictMode>{share ? <PublicReport token={share[1]} /> : path === '/status' || path === '/policies' ? <BetaPages statusPage={path === '/status'} /> : <><App /><footer className="beta-footer"><BetaLinks /></footer></>}</StrictMode>)
