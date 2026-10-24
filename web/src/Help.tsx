import { Modal } from './Modal';
import { t, useLanguage } from './i18n';

// How many explained items each page has; the texts are help.<page>.item<n>Title/Text.
const items = {
  send: 6,
  library: 6,
  shares: 6,
  admin: 7,
  public: 4,
  request: 2,
  signin: 3,
} as const;
export type HelpPage = keyof typeof items;
type Key = Parameters<typeof t>[0];

// Explains what is on the page being shown, opened from the top bar.
export function HelpDialog({ page, close }: { page: HelpPage; close: () => void }) {
  useLanguage();
  const text = (key: string) => t(('help.' + page + '.' + key) as Key);
  return (
    <Modal title={text('title')} close={close} className="help-dialog">
      <p className="muted">{text('intro')}</p>
      <dl className="help-list">
        {Array.from({ length: items[page] }, (_, i) => (
          <div key={i}>
            <dt>{text('item' + (i + 1) + 'Title')}</dt>
            <dd>{text('item' + (i + 1) + 'Text')}</dd>
          </div>
        ))}
      </dl>
    </Modal>
  );
}
