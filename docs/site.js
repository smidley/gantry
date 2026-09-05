const copyButton = document.querySelector('#copy-install');
const command = document.querySelector('#install-command');
const status = document.querySelector('#copy-status');

if (copyButton && command && status && navigator.clipboard?.writeText) {
  copyButton.hidden = false;
  copyButton.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(command.textContent);
      copyButton.textContent = 'Copied!';
      status.textContent = 'Installation command copied.';
    } catch {
      status.textContent = 'Copy was unavailable. Select and copy the command above.';
    }
  });
}
