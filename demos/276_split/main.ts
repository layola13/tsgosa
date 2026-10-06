function main(): number {
  const f = "a,b,c".split(",");
  for (const s of f) {
    console.log(s);
  }
  const g = "hello".split(";");
  return f.length + g.length * 10;
}
console.log(main());
