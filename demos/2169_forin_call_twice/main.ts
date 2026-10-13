interface O { a: number; b: number; }
interface T { p: number; q: number; r: number; }
function getObj(): O { return { a: 1, b: 2 }; }
function getT(): T { return { p: 1, q: 2, r: 3 }; }
function main(): number {
  let t = 0;
  for (const k in getObj()) { t += 1; }
  for (const k in getT()) { t += 1; }
  return t;
}
console.log(main())
