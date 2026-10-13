interface O { a: number; b: number; }
function main(c: boolean): number {
  const o1: O = { a: 1, b: 2 };
  const o2: O = { a: 3, b: 4 };
  let t = 0;
  for (const k in (c ? o1 : o2)) { t += 1; }
  return t;
}
console.log(main(true))
