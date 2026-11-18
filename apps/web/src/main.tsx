import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import PublicReport from './PublicReport'
import './styles.css'

const share = /^\/share\/([A-Za-z0-9_-]+)\/?$/.exec(window.location.pathname)
createRoot(document.getElementById('root')!).render(<StrictMode>{share ? <PublicReport token={share[1]} /> : <App />}</StrictMode>)
