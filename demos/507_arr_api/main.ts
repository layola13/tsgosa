function main(): i32 {
  const a = Array.from([1, 2, 3]);
  console.log(a[2]);
  const f = [1, 2, 3, 4].filter((x) => x > 2);
  console.log(f.length);
  console.log(f[0]);
  const g = [5, 6, 7];
  console.log(g.includes(6) ? 1 : 0);
  console.log(g.indexOf(7));
  console.log(g.join("-"));
  const h = [1, 2];
  h.push(3);
  console.log(h.length);
  console.log(h.pop());
  console.log(h.length);
  return 0;
}
