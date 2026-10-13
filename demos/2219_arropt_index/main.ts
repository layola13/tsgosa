function main(): number {
  const a = [10, 20, 30];
  return a?.[1] ?? -1;
}
console.log(main())
