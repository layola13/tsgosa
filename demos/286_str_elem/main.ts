function main(): number {
  const f: string[] = ["ab", "cd"];
  console.log(f[0]);
  console.log(f[1]);
  const g = f[0].split("b");
  return g.length;
}
console.log(main());
