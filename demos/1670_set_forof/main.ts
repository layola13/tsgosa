function main(): i32 {
  const st = new Set<i32>();
  st.add(1); st.add(2);
  let s = 0;
  for (const v of st) { s = s + v; }
  console.log(s);
  return 0;
}
