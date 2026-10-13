interface S { s: string; n: number; }
function main(): number {
  const o: S = { s: "x", n: 5 };
  return o?.["s"] ?? -1;
}
console.log(main())
