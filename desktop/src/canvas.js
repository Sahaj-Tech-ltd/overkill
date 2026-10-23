/* Shared, deliberately small catalog renderer. State never supplies markup. */
(() => {
  'use strict';
  const catalog = 'overkill.canvas.v1';
  const bytes = value => new TextEncoder().encode(value).length;
  const types = new Set(['Text', 'Heading', 'Column', 'Row', 'Divider']);
  function binding(data, pointer) {
    if (typeof pointer !== 'string' || !pointer.startsWith('/') || /~(?:[^01]|$)/.test(pointer)) throw new Error('Invalid canvas text binding.');
    let value = data;
    for (const part of pointer.slice(1).split('/')) {
      const key = part.replace(/~1/g, '/').replace(/~0/g, '~');
      if (!value || typeof value !== 'object' || !Object.hasOwn(value, key)) return '[Missing value]';
      value = value[key];
    }
    return value === null ? 'null' : ['string', 'number', 'boolean'].includes(typeof value) ? String(value) : '[Value cannot be displayed]';
  }
  function render(container, state, surfaceId) {
    if (!container || !state || typeof state.sessionId !== 'string' || !Number.isSafeInteger(state.revision) || state.revision < 0 || !Array.isArray(state.surfaces) || state.surfaces.length > 16 || bytes(JSON.stringify(state)) > 262144) throw new Error('Invalid canvas state.');
    const fragment = document.createDocumentFragment();
    const surfaces = surfaceId === undefined ? state.surfaces : state.surfaces.filter(surface => surface.surfaceId === surfaceId);
    if (surfaceId !== undefined && surfaces.length !== 1) throw new Error('Canvas surface is unavailable.');
    const ids = new Set();
    for (const surface of surfaces) {
      if (!surface || typeof surface.surfaceId !== 'string' || !surface.surfaceId || ids.has(surface.surfaceId) || surface.catalog !== catalog || !Array.isArray(surface.components) || surface.components.length > 128 || bytes(JSON.stringify(surface.data)) > 65536 || typeof surface.root !== 'string' || !surface.data || typeof surface.data !== 'object' || Array.isArray(surface.data) || typeof surface.rendering !== 'boolean') throw new Error('Unsupported or invalid canvas surface.');
      ids.add(surface.surfaceId);
      const nodes = new Map();
      for (const component of surface.components) {
        if (!component || typeof component.id !== 'string' || !component.id || nodes.has(component.id) || !types.has(component.type)) throw new Error('Unsupported or invalid canvas component.');
        const allowed = new Set(['id', 'type', ...(['Text', 'Heading'].includes(component.type) ? ['text', 'textPath'] : ['Column', 'Row'].includes(component.type) ? ['children'] : [])]);
        if (Object.keys(component).some(key => !allowed.has(key))) throw new Error('Unknown canvas component property.');
        if (['Text', 'Heading'].includes(component.type)) {
          if (Object.hasOwn(component, 'text') === Object.hasOwn(component, 'textPath') || (Object.hasOwn(component, 'text') && typeof component.text !== 'string') || (Object.hasOwn(component, 'textPath') && typeof component.textPath !== 'string')) throw new Error('Canvas text needs exactly one source.');
          const text = Object.hasOwn(component, 'text') ? component.text : binding(surface.data, component.textPath);
          if (bytes(text) > 16384) throw new Error('Canvas text is too large.');
        }
        if (['Column', 'Row'].includes(component.type) && (!Array.isArray(component.children) || component.children.length > 128 || component.children.some(id => typeof id !== 'string'))) throw new Error('Invalid canvas children.');
        nodes.set(component.id, component);
      }
      const heights = new Map(), active = new Set();
      function check(id) {
        if (active.has(id) || !nodes.has(id)) throw new Error('Invalid canvas component graph.');
        if (heights.has(id)) return heights.get(id);
        active.add(id);
        let height = 1;
        for (const child of nodes.get(id).children || []) height = Math.max(height, 1 + check(child));
        active.delete(id);
        if (height > 16) throw new Error('Canvas component tree is too deep.');
        heights.set(id, height); return height;
      }
      for (const id of nodes.keys()) check(id);
      if (surface.rendering && !nodes.has(surface.root)) throw new Error('Canvas root is unavailable.');
      const section = document.createElement('section');
      section.className = 'canvas-surface'; section.dataset.surfaceId = surface.surfaceId;
      let count = 0;
      function node(id, path) {
        if (++count > 2048 || path.length >= 16 || path.includes(id) || !nodes.has(id)) throw new Error('Invalid canvas component graph.');
        const component = nodes.get(id);
        const element = document.createElement(component.type === 'Heading' ? 'h3' : component.type === 'Divider' ? 'hr' : 'div');
        element.className = 'canvas-' + component.type.toLowerCase(); element.dataset.componentId = id;
        if (['Text', 'Heading'].includes(component.type)) {
          const text = Object.hasOwn(component, 'text') ? component.text : binding(surface.data, component.textPath);
          if (bytes(text) > 16384) throw new Error('Canvas text is too large.');
          element.textContent = text;
        } else if (component.children) {
          for (const child of component.children) element.append(node(child, [...path, id]));
        }
        return element;
      }
      // Validate even a not-yet-visible graph before committing the document.
      if (surface.rendering) section.append(node(surface.root, []));
      else { section.textContent = 'This surface has not started rendering.'; section.classList.add('canvas-empty'); }
      fragment.append(section);
    }
    if (!surfaces.length) { const empty = document.createElement('p'); empty.className = 'canvas-empty'; empty.textContent = 'No canvas surfaces in this chat.'; fragment.append(empty); }
    container.replaceChildren(fragment);
    container.dataset.canvasRevision = String(state.revision);
    container.dataset.surfaceId = surfaceId === undefined ? '' : surfaceId;
  }
  window.OverkillCanvas = Object.freeze({catalog, render});
})();
