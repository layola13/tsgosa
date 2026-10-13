function f(): void { }
function main(): number {
  return (f() as unknown as { a: number })?.["a"] ?? -1;
}
console.log(main())
