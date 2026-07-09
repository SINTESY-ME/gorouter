/** @type {import('tailwindcss').Config} */
const { heroui } = require("@heroui/react");

export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
    "./node_modules/@heroui/react/node_modules/@heroui/*/dist/**/*.{js,ts,jsx,tsx,mjs}",
  ],
  theme: {
    extend: {},
  },
  darkMode: "class",
  plugins: [heroui()],
};