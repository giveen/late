package tui

var LateTheme = []byte(`
{
  "document": {
    "block_prefix": "",
    "block_suffix": "",
    "color": "#ECF0F1",
    "background_color": "#191919",
    "margin": 0
  },
  "paragraph": {
    "margin": 0,
    "color": "#ECF0F1",
    "background_color": "#191919"
  },
  "block_quote": {
    "indent": 1,
    "indent_token": "│ ",
    "color": "#BDC3C7",
    "background_color": "#191919"
  },
  "list": {
    "level_indent": 2,
    "color": "#ECF0F1",
    "background_color": "#191919"
  },
  "heading": {
    "block_suffix": "\n",
    "color": "#9B59B6",
    "background_color": "#191919",
    "bold": true
  },
  "h1": {
    "prefix": "# ",
    "background_color": "#191919"
  },
  "h2": {
    "prefix": "## ",
    "background_color": "#191919"
  },
  "h3": {
    "prefix": "### ",
    "background_color": "#191919"
  },
  "strong": {
    "bold": true,
    "color": "#E67E22",
    "background_color": "#191919"
  },
  "emph": {
    "italic": true,
    "color": "#F1C40F",
    "background_color": "#191919"
  },
  "code": {
    "color": "#2ECC71",
    "background_color": "#191919"
  },
  "code_block": {
    "margin": 0,
    "chroma": {
      "text": {
        "color": "#ECF0F1"
      },
      "error": {
        "color": "#F1F1F1",
        "background_color": "#F05252"
      },
      "comment": {
        "color": "#7F8C8D"
      },
      "keyword": {
        "color": "#9B59B6"
      },
      "literal": {
        "color": "#2ECC71"
      },
      "name_tag": {
        "color": "#2980B9"
      },
      "operator": {
        "color": "#ECF0F1"
      },
      "string": {
        "color": "#F1C40F"
      }
    },
    "background_color": "#191919"
  },
  "table": {
    "center": true,
    "color": "#ECF0F1",
    "background_color": "#191919"
  },
  "link": {
    "color": "#3498DB",
    "underline": true,
    "background_color": "#191919"
  },
  "image": {
    "color": "#3498DB",
    "underline": true,
    "background_color": "#191919"
  }
}
`)
