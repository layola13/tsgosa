interface O { a: number; b: number; }
let c = 0;
function dflt(): number { c += 1; return -1; }
function main(): number {
  const o: O = { a: 10, b: 32 };
  const v = o?.["a"] ?? dflt();
  return v * 100 + c;
}
console.log(main())
