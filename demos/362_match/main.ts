function main(): i32 {
  const s: string = "a1b2";
  const m = s.match(/1/);
  console.log(m.length);
  console.log(m[0].length);
  console.log(m[0]);
  const re = /1/;
  const e = re.exec("a1b2");
  console.log(e.length);
  console.log(e[0]);
  const g = s.match(/[0-9]/g);
  console.log(g.length);
  console.log(g[1]);
  return 0;
}
