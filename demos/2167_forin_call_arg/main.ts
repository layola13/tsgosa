interface T { p: number; q: number; r: number; }
function getT(n: number): T { return { p: n, q: n, r: n }; }
function main(): number {
  let t = 0;
  for (const k in getT(5)) { t += 1; }
  return t;
}
console.log(main())
