// Two elements picked by the daemon's picker (internal/agent/pick.go) in a
// real Chromium from dev/pickDemo.ts's page, at 1440×900: the Save button and
// the header's Upgrade button, crops and all.
import type * as T from '../../shared/api';

export const pickedSave: T.ElementPick = {
  "url": "http://localhost:3000/settings/",
  "title": "Settings · Pawly",
  "selector": "#save",
  "tag": "button",
  "html": "<button id=\"save\" class=\"btn-save\" type=\"submit\">Save changes</button>",
  "text": "Save changes",
  "styles": [
    {
      "name": "display",
      "value": "block"
    },
    {
      "name": "width",
      "value": "131.641px"
    },
    {
      "name": "height",
      "value": "41px"
    },
    {
      "name": "padding",
      "value": "9px 16px"
    },
    {
      "name": "color",
      "value": "rgb(59, 64, 84)"
    },
    {
      "name": "background-color",
      "value": "rgb(233, 234, 240)"
    },
    {
      "name": "font-family",
      "value": "system-ui, -apple-system, \"Segoe UI\", sans-serif"
    },
    {
      "name": "font-size",
      "value": "14px"
    },
    {
      "name": "font-weight",
      "value": "400"
    },
    {
      "name": "line-height",
      "value": "21px"
    },
    {
      "name": "text-align",
      "value": "center"
    },
    {
      "name": "border",
      "value": "1px solid rgb(154, 160, 180)"
    },
    {
      "name": "border-radius",
      "value": "3px"
    },
    {
      "name": "cursor",
      "value": "default"
    }
  ],
  "box": {
    "x": 895,
    "y": 469,
    "width": 132,
    "height": 41
  },
  "viewport": {
    "width": 1440,
    "height": 747
  },
  "image": {
    "mimeType": "image/png",
    "data": "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",
    "name": "element.png"
  }
};

export const pickedUpgrade: T.ElementPick = {
  "url": "http://localhost:3000/settings/",
  "title": "Settings · Pawly",
  "selector": "button.btn-upgrade",
  "tag": "button",
  "html": "<button class=\"btn-upgrade\" type=\"button\">Upgrade to Pro</button>",
  "text": "Upgrade to Pro",
  "styles": [
    {
      "name": "display",
      "value": "block"
    },
    {
      "name": "width",
      "value": "137.125px"
    },
    {
      "name": "height",
      "value": "32px"
    },
    {
      "name": "padding",
      "value": "8px 18px"
    },
    {
      "name": "color",
      "value": "rgb(255, 255, 255)"
    },
    {
      "name": "background-image",
      "value": "linear-gradient(135deg, rgb(139, 92, 246), rgb(99, 102, 241))"
    },
    {
      "name": "font-family",
      "value": "Arial"
    },
    {
      "name": "font-size",
      "value": "14px"
    },
    {
      "name": "font-weight",
      "value": "600"
    },
    {
      "name": "text-align",
      "value": "center"
    },
    {
      "name": "border-radius",
      "value": "999px"
    },
    {
      "name": "box-shadow",
      "value": "rgba(99, 102, 241, 0.45) 0px 4px 14px 0px"
    },
    {
      "name": "cursor",
      "value": "pointer"
    }
  ],
  "box": {
    "x": 1263,
    "y": 16,
    "width": 137,
    "height": 32
  },
  "viewport": {
    "width": 1440,
    "height": 747
  },
  "image": {
    "mimeType": "image/png",
    "data": "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",
    "name": "element.png"
  }
};
