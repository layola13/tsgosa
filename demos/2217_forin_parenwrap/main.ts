interface O { a: number; b: number; }
function main(): number {
  const o: O = { a: 1, b: 2 };
  let t = 0;
  for (const k in ((o))) { t += 1; }
  return t;
}
console.log(main())
