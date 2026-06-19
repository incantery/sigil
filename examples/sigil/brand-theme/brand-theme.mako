theme dawn extends light =
  primary = "#ff6b35" on "#0a0a0a"
  accent  = "#0f766e" on "#ffffff"

theme dawn-dark extends dark =
  primary = "#ff8c5a" on "#0a0a0a"
  accent  = "#5eead4" on "#0a0a0a"

view BrandThemeDemo =
  state name = "Sigil"
  card
    title "Custom brand theme"
    text "Primary and accent are overridden in source; surface/danger/success/warning fall through from light/dark defaults."
    stack horizontal gap=1
      button "Primary" tone=primary
      button "Accent"  tone=accent
      button "Danger"  tone=danger
      button "Success" tone=success
