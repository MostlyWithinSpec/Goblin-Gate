import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { MemoryRouter } from 'react-router-dom';
import Network from '../pages/Network';

describe('Network', () => {
  test('renders network title', () => {
    render(
      <MemoryRouter>
        <Network />
      </MemoryRouter>
    );
    const titleElement = screen.getByText(/Network Configuration/i);
    expect(titleElement).toBeInTheDocument();
  });

  test('links to the network sub-pages', () => {
    render(
      <MemoryRouter>
        <Network />
      </MemoryRouter>
    );
    for (const name of ['Port Forwards', 'DHCP', 'DNS']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument();
    }
  });
});