interface O { a: number; b: number; }
function getObj(): O { return { a: 1, b: 2 }; }
function main(): string {
  let s = "";
  for (const k in getObj()) { s += k; }
  return s;
}
console.log(main())
