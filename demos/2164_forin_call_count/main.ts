interface O { a: number; b: number; }
function getObj(): O { return { a: 1, b: 2 }; }
function main(): number {
  let t = 0;
  for (const k in getObj()) { t += 1; }
  return t;
}
console.log(main())
