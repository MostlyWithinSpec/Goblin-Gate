import React from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'
import { applyTheme, savedTheme } from './theme'
import { initialLanguage, setLanguage } from './i18n'

// before the first render, so login and setup start in the right theme too
applyTheme(savedTheme())

// the language's catalog loads before the first render (English is built in)
setLanguage(initialLanguage())
  .catch(() => setLanguage('en'))
  .finally(() =>
    createRoot(document.getElementById('root')!).render(
      <React.StrictMode>
        <App />
      </React.StrictMode>,
    ),
  )
