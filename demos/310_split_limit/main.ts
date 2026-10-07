function main(): number {
  const f = "a,b,c".split(",", 2);
  const g = "hello".split(";", 5);
  const h = "a,b,c".split(",", 0);
  const k = "a,b,c".split(",", -1);
  console.log(f.length);
  console.log(g.length);
  console.log(h.length);
  console.log(k.length);
  return f.length + g.length * 10 + h.length * 100 + k.length * 1000;
}
console.log(main());
