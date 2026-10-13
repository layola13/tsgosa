interface O { a: number; b: number; }
function getObj(n: number): O | null {
  if (n > 0) { return { a: n, b: n }; }
  return null;
}
function main(): number {
  return getObj(5)?.["a"] ?? -1;
}
console.log(main())
