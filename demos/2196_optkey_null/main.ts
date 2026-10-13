interface O { a: number; b: number; }
function main(): number {
  const n: O | null = null;
  return n?.["a"] ?? -1;
}
console.log(main())
