function main(): number {
  const f: string[] = ["a", "b"];
  f[0] = "z";
  const s = f[0] + "!";
  console.log(s);
  return s.length;
}
console.log(main());
