function main(): i32 {
  const st = new Set<string>();
  st.add("a"); st.add("b"); st.add("a");
  console.log(st.size);
  console.log(st.has("b") ? 1 : 0);
  return 0;
}
