interface O { a: number; }
function main(): number {
  const n = 5;
  return (n as unknown as O)["a"];
}
console.log(main())
