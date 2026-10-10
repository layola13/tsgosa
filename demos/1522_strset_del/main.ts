function main(): i32 {
  const st = new Set<string>();
  st.add("a"); st.add("b");
  st.delete("a");
  console.log(st.size);
  console.log(st.has("a") ? 1 : 0);
  return 0;
}
