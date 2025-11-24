import type { SxProps, Theme } from '@mui/material';

/**
 * Swagger UI ships a light-only stylesheet, so on a dark background it renders
 * as a white slab with black text. These overrides repaint its surfaces from the
 * MUI palette instead of inverting the whole thing with a filter, which would
 * also invert the method badges and syntax colours that carry meaning.
 *
 * Everything is scoped under `.swagger-ui` and applied only in the dark scheme,
 * so the light rendering is left exactly as upstream designed it.
 */
export const swaggerSx: SxProps<Theme> = {
  // Chrome this app already provides.
  '.swagger-ui .topbar': { display: 'none' },
  '.swagger-ui .info': { margin: '1rem 0' },
  '.swagger-ui .scheme-container': { background: 'transparent', boxShadow: 'none', padding: '0 0 1rem' },

  '@media (prefers-color-scheme: dark)': {
    '.swagger-ui': {
      color: 'text.primary',
      // Text that upstream sets in near-black, in one sweep.
      '.info .title, .info li, .info p, .info table, .opblock-tag, .opblock .opblock-summary-path, .opblock .opblock-summary-path__deprecated, .opblock .opblock-summary-description, .opblock-description-wrapper p, .opblock-external-docs-wrapper p, .opblock-title_normal p, .parameter__name, .parameter__type, .parameter__in, .parameter__extension, .response-col_status, .response-col_links, .responses-inner h4, .responses-inner h5, .tab li, .btn, label, table thead tr td, table thead tr th, .model-title, .model, .prop-type, .prop-format, .servers-title, .servers > label, .dialog-ux .modal-ux-content h4, .dialog-ux .modal-ux-content p, .dialog-ux .modal-ux-header h3, .loading-container .loading::after':
        { color: 'text.primary' },
      '.opblock .opblock-section-header h4, .opblock-summary-method': { color: 'common.white' },
      'svg:not(:root)': { fill: 'currentColor' },
    },

    // Panels, borders and the section headers inside each operation.
    '.swagger-ui .opblock': {
      background: 'background.paper',
      border: 1,
      borderColor: 'divider',
      boxShadow: 'none',
    },
    '.swagger-ui .opblock .opblock-section-header': {
      background: 'action.hover',
      boxShadow: 'none',
    },
    '.swagger-ui .opblock-tag': { borderBottomColor: 'divider' },
    '.swagger-ui section.models': { background: 'background.paper', borderColor: 'divider' },
    '.swagger-ui section.models .model-container': { background: 'action.hover' },
    '.swagger-ui .model-box': { background: 'action.hover' },
    '.swagger-ui .dialog-ux .modal-ux': { background: 'background.paper', borderColor: 'divider' },
    '.swagger-ui .dialog-ux .modal-ux-header': { borderBottomColor: 'divider' },

    // Tables, inputs and the try-it-out form.
    '.swagger-ui table thead tr td, .swagger-ui table thead tr th': { borderBottomColor: 'divider' },
    '.swagger-ui input[type=email], .swagger-ui input[type=file], .swagger-ui input[type=password], .swagger-ui input[type=search], .swagger-ui input[type=text], .swagger-ui textarea, .swagger-ui select':
      {
        background: 'background.default',
        color: 'text.primary',
        borderColor: 'divider',
      },
    '.swagger-ui .parameter__name.required::after': { color: 'error.main' },

    // Response and example bodies. Upstream renders these on white with a very
    // dark syntax theme, which is the worst offender in a dark page.
    '.swagger-ui .highlight-code > .microlight, .swagger-ui .responses-inner pre, .swagger-ui .body-param__example':
      {
        background: '#11141a',
        color: '#e6e9ef',
      },
    '.swagger-ui .microlight code': { color: 'inherit' },

    // Buttons that upstream draws as dark-on-white.
    '.swagger-ui .btn': { background: 'transparent', borderColor: 'divider' },
    '.swagger-ui .btn:hover': { background: 'action.hover' },
    '.swagger-ui .btn.authorize': { color: 'success.light', borderColor: 'success.light' },
    '.swagger-ui .btn.authorize svg': { fill: 'currentColor' },
    '.swagger-ui .btn.execute': { background: 'primary.main', borderColor: 'primary.main', color: 'common.white' },
    '.swagger-ui .tab li.active': { color: 'primary.main' },

    // The expand/collapse arrows and the copy icon are black SVGs by default.
    '.swagger-ui .expand-methods svg, .swagger-ui .expand-operation svg, .swagger-ui .arrow': {
      fill: 'text.secondary',
    },
  },
};
