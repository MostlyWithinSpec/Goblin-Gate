import React from 'react'

// Button takes every native <button> attribute (onClick with the event,
// type, title, aria-label, ...). type defaults to "button" so a Button
// inside a form does not submit it by accident.
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'outline'
  size?: 'sm' | 'md' | 'lg' | 'icon'
}

const base =
  'inline-flex items-center justify-center gap-1 rounded-md font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 disabled:opacity-50 disabled:pointer-events-none'

const variants = {
  primary: 'bg-blue-600 text-white hover:bg-blue-700 focus-visible:ring-blue-500',
  secondary: 'bg-gray-200 text-gray-900 hover:bg-gray-300 focus-visible:ring-gray-500 dark:bg-gray-700 dark:text-white dark:hover:bg-gray-600',
  danger: 'bg-red-600 text-white hover:bg-red-700 focus-visible:ring-red-500',
  ghost: 'bg-transparent text-gray-700 hover:bg-gray-100 focus-visible:ring-gray-500 dark:text-gray-200 dark:hover:bg-gray-700',
  outline: 'border border-gray-300 bg-transparent text-gray-800 hover:bg-gray-50 focus-visible:ring-gray-500 dark:border-gray-600 dark:text-gray-100 dark:hover:bg-gray-700',
}

const sizes = {
  sm: 'text-xs px-2 py-1',
  md: 'text-sm px-4 py-2',
  lg: 'text-base px-5 py-2.5',
  icon: 'p-1.5',
}

export const Button: React.FC<ButtonProps> = ({ variant = 'secondary', size = 'md', className = '', type = 'button', ...rest }) => (
  <button type={type} className={`${base} ${variants[variant]} ${sizes[size]} ${className}`} {...rest} />
)
