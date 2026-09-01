(() => {
  const toggle = document.querySelector("[data-nav-toggle]");
  const nav = document.querySelector("[data-site-nav]");
  if (toggle && nav) {
    toggle.addEventListener("click", () => {
      const open = nav.getAttribute("data-open") === "true";
      nav.setAttribute("data-open", String(!open));
      toggle.setAttribute("aria-expanded", String(!open));
    });
    nav.addEventListener("click", (event) => {
      if (event.target instanceof HTMLAnchorElement) {
        nav.setAttribute("data-open", "false");
        toggle.setAttribute("aria-expanded", "false");
      }
    });
  }

  for (const element of document.querySelectorAll("[data-current-year]")) {
    element.textContent = String(new Date().getFullYear());
  }
})();
