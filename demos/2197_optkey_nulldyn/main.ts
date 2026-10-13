interface O { a: number; b: number; }
function main(): number {
  const n: O | null = { a: 7, b: 8 };
  const k = "b";
  return n?.[k] ?? -1;
}
console.log(main())
