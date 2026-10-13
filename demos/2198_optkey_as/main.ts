interface O { a: number; b: number; }
function main(): number {
  const o: O = { a: 10, b: 32 };
  return (o as O)?.["b"] ?? -1;
}
console.log(main())
