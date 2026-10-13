interface O { a: number; }
function getN(): number { return 3; }
function main(): number {
  return (getN() as unknown as O)["a"];
}
console.log(main())
