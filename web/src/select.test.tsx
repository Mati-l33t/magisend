import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import { Select } from './Select';

function Picker({ changed }: { changed: (value: number) => void }) {
  const [value, setValue] = useState(3);
  return (
    <Select
      label="Downloads"
      value={value}
      onChange={(next) => {
        setValue(next);
        changed(next);
      }}
      options={[
        { value: 0, label: 'No limit' },
        { value: 1, label: 'Once' },
        { value: 3, label: '3 times' },
        { value: 5, label: '5 times' },
      ]}
    />
  );
}

it('is an accessible combobox driven by the keyboard', () => {
  const changed = vi.fn();
  const outer = vi.fn();
  render(
    <div onKeyDown={outer}>
      <Picker changed={changed} />
    </div>,
  );
  const box = screen.getByRole('combobox', { name: 'Downloads' });
  expect(box).toHaveTextContent('3 times');
  expect(box).toHaveAttribute('aria-expanded', 'false');
  fireEvent.keyDown(box, { key: 'ArrowDown' });
  expect(box).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByRole('option', { name: '3 times' })).toHaveAttribute('aria-selected', 'true');
  fireEvent.keyDown(box, { key: 'ArrowDown' });
  expect(box.getAttribute('aria-activedescendant')).toBe(
    screen.getByRole('option', { name: '5 times' }).id,
  );
  fireEvent.keyDown(box, { key: 'Enter' });
  expect(changed).toHaveBeenLastCalledWith(5);
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  // Escape closes only the list; a surrounding dialog never sees it.
  fireEvent.keyDown(box, { key: 'Home' });
  fireEvent.keyDown(box, { key: 'Escape' });
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  expect(outer.mock.calls.some(([event]) => event.key === 'Escape')).toBe(false);
  expect(changed).toHaveBeenCalledTimes(1);
  // Typing picks the first matching option while closed.
  fireEvent.keyDown(box, { key: 'o' });
  expect(changed).toHaveBeenLastCalledWith(1);
  fireEvent.click(box);
  fireEvent.click(screen.getByRole('option', { name: 'No limit' }));
  expect(changed).toHaveBeenLastCalledWith(0);
});
