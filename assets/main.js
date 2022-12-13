/**
 * sends value to iframe parent
 * @param {string} url
 */
function handleClick(url) {

  const params = new Proxy(new URLSearchParams(window.location.search), {
    get: (searchParams, prop) => searchParams.get(prop),
  });

  const id = params.id;

  navigator.clipboard.writeText(url);
  window.parent.postMessage({
    id: id,
    value: url,
  }, '*');

}
