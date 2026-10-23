'use strict';
window.renderCanvasSnapshot = (encoded, surfaceID) => {
  const state = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(encoded), character => character.charCodeAt(0))));
  const root = document.getElementById('canvas-root');
  window.OverkillCanvas.render(root, state, surfaceID);
  return {revision: Number(root.dataset.canvasRevision), surfaceID: root.dataset.surfaceId};
};
