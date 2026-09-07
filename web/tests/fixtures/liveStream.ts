import type { Page } from '@playwright/test';

// A fulfilled SSE response ends immediately and makes EventSource reconnect.
// UI fixtures instead deliver their mocked snapshot through a connected stream;
// the real transport and session lifecycle have separate integration coverage.
export async function mockLiveStream(page: Page) {
  await page.addInitScript(() => {
    class SnapshotStream extends EventTarget {
      onopen: ((event: Event) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      timer: ReturnType<typeof setInterval>;
      closed = false;
      opened = false;

      constructor(_url: string) {
        super();
        const send = async () => {
          try {
            const response = await fetch('/api/live/snapshot');
            if (!response.ok) throw new Error(`Snapshot returned ${response.status}`);
            const frame = await response.json();
            if (this.closed) return;
            if (!this.opened) {
              this.opened = true;
              this.onopen?.(new Event('open'));
            }
            this.dispatchEvent(new MessageEvent('frame', { data: JSON.stringify(frame) }));
          } catch {
            if (this.closed) return;
            this.opened = false;
            this.onerror?.(new Event('error'));
          }
        };
        this.timer = setInterval(send, 1000);
        void send();
      }

      close() {
        this.closed = true;
        clearInterval(this.timer);
      }
    }
    window.EventSource = SnapshotStream as unknown as typeof EventSource;
  });
}
