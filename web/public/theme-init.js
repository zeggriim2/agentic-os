(function () {
  try {
    var t = localStorage.getItem("aos-theme");
    var root = document.documentElement;
    if (t === "light") root.classList.remove("dark");
    else root.classList.add("dark");
  } catch (e) {
    /* stockage indisponible : thème sombre par défaut */
  }
})();
