interface O { a: number; b: number; }
function main(): number {
  const o: O = { a: 10, b: 32 };
  const k = "a";
  return (o as any)[k];
}
console.log(main())
