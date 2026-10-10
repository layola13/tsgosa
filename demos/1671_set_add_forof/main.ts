function main(): i32 {
  const st = new Set();
  st.add(5);
  st.add(7);
  let s = 0;
  for (const v of st) { s = s + v; }
  console.log(s);
  return 0;
}
