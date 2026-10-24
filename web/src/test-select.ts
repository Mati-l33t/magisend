import { fireEvent, screen } from '@testing-library/react';

// Picks an option in the app's own dropdown (Select.tsx).
export function choose(name: string | RegExp, option: string | RegExp) {
  fireEvent.click(screen.getByRole('combobox', { name }));
  fireEvent.click(screen.getByRole('option', { name: option }));
}

export function selectedOption(name: string | RegExp) {
  return screen.getByRole('combobox', { name }).textContent;
}
