function main(): i32 {
  const st = new Set<i32>();
  for (let i = 0; i < 3; i = i + 1) { st.add(i * 10); }
  console.log(st.size);
  console.log(st.has(20) ? 1 : 0);
  return 0;
}
