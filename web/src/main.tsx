import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App.tsx'
import Preview from './Preview.tsx'
import './index.css'

// ?preview — офлайн-витрина всех экранов (обе темы, мок-данные, без Telegram/авторизации).
const isPreview = new URLSearchParams(window.location.search).has('preview')

createRoot(document.getElementById('root')!).render(
  <StrictMode>{isPreview ? <Preview /> : <App />}</StrictMode>,
)
